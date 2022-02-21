package apply

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strconv"

	survey "github.com/AlecAivazis/survey/v2"
	"github.com/olekukonko/tablewriter"
	"github.com/pquerna/ffjson/ffjson"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	resourcehelper "k8s.io/kubectl/pkg/util/resource"
	"sigs.k8s.io/yaml"

	gpusharecache "github.com/alibaba/open-gpu-share/pkg/cache"
	gpushareutils "github.com/alibaba/open-gpu-share/pkg/utils"
	localcache "github.com/alibaba/open-local/pkg/scheduler/algorithm/cache"
	"github.com/alibaba/open-simulator/pkg/api/v1alpha1"
	"github.com/alibaba/open-simulator/pkg/chart"
	"github.com/alibaba/open-simulator/pkg/simulator"
	simontype "github.com/alibaba/open-simulator/pkg/type"
	"github.com/alibaba/open-simulator/pkg/utils"
)

type Options struct {
	SimonConfig                string
	DefaultSchedulerConfigFile string
	UseGreed                   bool
	Interactive                bool
	ExtendedResources          []string
}

type Applier struct {
	cluster           v1alpha1.Cluster
	appList           []v1alpha1.AppInfo
	newNode           string
	schedulerConfig   string
	useGreed          bool
	interactive       bool
	extendedResources []string
}

type Interface interface {
	Run() error
}

// NewApplier returns a default applier that has passed the validity test
func NewApplier(opts Options) Interface {
	simonCR := &v1alpha1.Simon{}
	configFile, err := ioutil.ReadFile(opts.SimonConfig)
	if err != nil {
		log.Fatalf("failed to read config file(%s): %v", opts.SimonConfig, err)
	}
	configJSON, err := yaml.YAMLToJSON(configFile)
	if err != nil {
		log.Fatalf("failed to unmarshal config file(%s) to json: %v", opts.SimonConfig, err)
	}

	if err := json.Unmarshal(configJSON, simonCR); err != nil {
		log.Fatalf("failed to unmarshal config json to object: %v", err)
	}

	applier := &Applier{
		cluster:           simonCR.Spec.Cluster,
		appList:           simonCR.Spec.AppList,
		newNode:           simonCR.Spec.NewNode,
		schedulerConfig:   opts.DefaultSchedulerConfigFile,
		useGreed:          opts.UseGreed,
		interactive:       opts.Interactive,
		extendedResources: opts.ExtendedResources,
	}

	if err := validate(applier); err != nil {
		fmt.Printf("%v", err)
		os.Exit(1)
	}

	return applier
}

func (applier *Applier) Run() (err error) {
	var (
		resourceMap  map[string]simulator.ResourceTypes
		resourceList []string
		content      []string
	)
	resourceMap = make(map[string]simulator.ResourceTypes)

	// Step 1: convert the application files into the kubernetes objects and generate a ResourceTypes struct, then make a resource list
	var appResource simulator.ResourceTypes
	for _, app := range applier.appList {
		// process separately chart and other file
		if app.Chart {
			// parse and render chart as a yaml array
			if content, err = chart.ProcessChart(app.Name, app.Path); err != nil {
				return err
			}
		} else {
			if content, err = utils.GetYamlContentFromDirectory(app.Path); err != nil {
				return err
			}
		}
		if appResource, err = simulator.GetObjectFromYamlContent(content); err != nil {
			return err
		}

		resourceMap[app.Name] = appResource
		resourceList = append(resourceList, app.Name)
	}

	// Step 2: convert the cluster files into the kubernetes objects and generate a ResourceTypes struct
	// cluster resource generated by two types of cluster, custom cluster and real cluster
	var clusterResource simulator.ResourceTypes
	if applier.cluster.KubeConfig != "" {
		// generate kube-client
		kubeclient, err := utils.CreateKubeClient(applier.cluster.KubeConfig)
		if err != nil {
			return err
		}
		if clusterResource, err = simulator.CreateClusterResourceFromClient(kubeclient); err != nil {
			return err
		}
	} else {
		if clusterResource, err = simulator.CreateClusterResourceFromClusterConfig(applier.cluster.CustomCluster); err != nil {
			return err
		}
	}

	// Step 3: convert the path of the new node to be added into the kubernetes object
	// only support temporarily one type of node at present
	var nodeResource simulator.ResourceTypes
	if content, err = utils.GetYamlContentFromDirectory(applier.newNode); err != nil {
		return err
	}
	if nodeResource, err = simulator.GetObjectFromYamlContent(content); err != nil {
		return err
	}
	if len(nodeResource.Nodes) == 0 {
		return fmt.Errorf("the new node directory(%s) has no nodes ", applier.newNode)
	}
	simulator.MatchAndSetLocalStorageAnnotationOnNode(nodeResource.Nodes, applier.newNode)

	// confirm the list of applications that needed to be deployed in interactive mode
	var selectedAppNameList []string
	var selectedResourceList []simulator.AppResource
	if applier.interactive {
		var multiQs = []*survey.Question{
			{
				Name: "APPs",
				Prompt: &survey.MultiSelect{
					Message: "Confirm your apps :",
					Options: resourceList,
				},
			},
		}
		err = survey.Ask(multiQs, &selectedAppNameList)
		if err != nil {
			log.Fatalf("%v", err)
		}
	} else {
		selectedAppNameList = resourceList
	}
	for _, name := range selectedAppNameList {
		selectedResourceList = append(selectedResourceList, simulator.AppResource{
			Name:     name,
			Resource: resourceMap[name],
		})
	}

	// Step 4: determining that the current cluster can deploy selected applications and meets the given requests,
	// If everything is ok, output the result. Otherwise we adjust the scale of cluster by adding node
	success := false
	// only support temporarily adding a type of node at present
	newNode := nodeResource.Nodes[0]
	var result *simulator.SimulateResult
	for i := 0; i < simontype.MaxNumNewNode; i++ {
		newClusterResource := clusterResource
		// add nodes to get a successful scheduling
		fmt.Printf(utils.ColorYellow+"add %d node(s)\n"+utils.ColorReset, i)
		nodes, err := newFakeNodes(newNode, i)
		if err != nil {
			return err
		}
		newClusterResource.Nodes = append(newClusterResource.Nodes, nodes...)

		// synchronize pods generated by deployment、daemonset and like this, then format all unscheduled pods
		result, err = simulator.Simulate(newClusterResource, selectedResourceList)
		if err != nil {
			return err
		}
		if len(result.UnscheduledPods) == 0 {
			if ok, reason, err := satisfyResourceSetting(result.NodeStatus); err != nil {
				return err
			} else if !ok {
				fmt.Printf(utils.ColorRed+"%s"+utils.ColorReset, reason)
			} else {
				success = true
				break
			}
		} else {
			fmt.Printf(utils.ColorRed+"there are %d unscheduled pods\n"+utils.ColorReset, len(result.UnscheduledPods))
			allDaemonSets := newClusterResource.DaemonSets
			for _, app := range selectedResourceList {
				allDaemonSets = append(allDaemonSets, app.Resource.DaemonSets...)
			}
			for _, unScheduledPod := range result.UnscheduledPods {
				log.Debugf("failed to schedule pod %s/%s: %s", unScheduledPod.Pod.Namespace, unScheduledPod.Pod.Name, unScheduledPod.Reason)
				if !utils.NodeShouldRunPod(newNode, unScheduledPod.Pod) {
					fmt.Printf(utils.ColorRed+"failed to schedule pod %s/%s: %s the pod cannot be scheduled successfully by adding node: pod does not fit new node affinity or taints\n"+utils.ColorReset, unScheduledPod.Pod.Namespace, unScheduledPod.Pod.Name, unScheduledPod.Reason)
					fmt.Printf(utils.ColorRed)
					report(result.NodeStatus, applier.extendedResources)
					fmt.Printf(utils.ColorReset)
					return nil
				}
				if m, err := utils.MeetResourceRequests(newNode, unScheduledPod.Pod, allDaemonSets); err != nil {
					return err
				} else if !m {
					fmt.Printf(utils.ColorRed+"failed to schedule pod %s/%s: new node cannot meet resource requests of pod: the total requested resource of daemonset pods in new node is too large\n"+utils.ColorReset, unScheduledPod.Pod.Namespace, unScheduledPod.Pod.Name)
					fmt.Printf(utils.ColorRed)
					report(result.NodeStatus, applier.extendedResources)
					fmt.Printf(utils.ColorReset)
					return nil
				}
			}
		}
	}

	if success {
		fmt.Printf(utils.ColorGreen + "Success!\n" + utils.ColorReset)
		fmt.Printf(utils.ColorGreen)
		report(result.NodeStatus, applier.extendedResources)
		fmt.Printf(utils.ColorReset)
	} else {
		fmt.Printf(utils.ColorRed + fmt.Sprintf("we have added %d nodes but it still failed!!", simontype.MaxNumNewNode) + utils.ColorReset)
	}

	return nil
}

func validate(applier *Applier) error {
	if len(applier.cluster.KubeConfig) == 0 && len(applier.cluster.CustomCluster) == 0 ||
		len(applier.cluster.KubeConfig) != 0 && len(applier.cluster.CustomCluster) != 0 {
		return fmt.Errorf("only one of values of both kubeConfig and customConfig must exist ")
	}

	if len(applier.cluster.KubeConfig) != 0 {
		if _, err := os.Stat(applier.cluster.KubeConfig); err != nil {
			return fmt.Errorf("invalid path of kubeConfig: %v ", err)
		}
	}

	if len(applier.cluster.CustomCluster) != 0 {
		if _, err := os.Stat(applier.cluster.CustomCluster); err != nil {
			return fmt.Errorf("invalid path of customConfig: %v ", err)
		}
	}

	if len(applier.schedulerConfig) != 0 {
		if _, err := os.Stat(applier.schedulerConfig); err != nil {
			return fmt.Errorf("invalid path of scheduler config: %v ", err)
		}
	}

	if len(applier.newNode) != 0 {
		if _, err := os.Stat(applier.newNode); err != nil {
			return fmt.Errorf("invalid path of newNode: %v ", err)
		}
	}

	for _, app := range applier.appList {
		if _, err := os.Stat(app.Path); err != nil {
			return fmt.Errorf("invalid path of %s app: %v ", app.Name, err)
		}
	}

	return nil
}

func newFakeNodes(node *corev1.Node, nodeCount int) ([]*corev1.Node, error) {
	if node == nil {
		return nil, fmt.Errorf("node is nil ")
	}

	var nodes []*corev1.Node
	// make fake nodes
	for i := 0; i < nodeCount; i++ {
		hostname := fmt.Sprintf("%s-%02d", simontype.NewNodeNamePrefix, i)
		validNode, err := utils.MakeValidNodeByNode(node, hostname)
		if err != nil {
			return nil, err
		}
		metav1.SetMetaDataLabel(&validNode.ObjectMeta, simontype.LabelNewNode, "")
		nodes = append(nodes, validNode.DeepCopy())
	}
	return nodes, nil
}

// report print out scheduling result of pods
func report(nodeStatuses []simulator.NodeStatus, extendedResources []string) {
	// Step 1: report pod info
	fmt.Println("Pod Info")
	podTable := tablewriter.NewWriter(os.Stdout)
	header := []string{
		"Node",
		"Pod",
		"CPU Requests",
		"Memory Requests",
	}
	if containLocalStorage(extendedResources) {
		header = append(header, "Volume Request")
	}
	if containGpu(extendedResources) {
		header = append(header, "GPU Mem Requests")
	}
	header = append(header, "APP Name")
	podTable.SetHeader(header)

	for _, status := range nodeStatuses {
		node := status.Node
		allocatable := node.Status.Allocatable
		for _, pod := range status.Pods {
			if pod.Spec.NodeName != node.Name {
				continue
			}
			req, limit := resourcehelper.PodRequestsAndLimits(pod)
			cpuReq, _, memoryReq, _ := req[corev1.ResourceCPU], limit[corev1.ResourceCPU], req[corev1.ResourceMemory], limit[corev1.ResourceMemory]
			fractionCpuReq := float64(cpuReq.MilliValue()) / float64(allocatable.Cpu().MilliValue()) * 100
			fractionMemoryReq := float64(memoryReq.Value()) / float64(allocatable.Memory().Value()) * 100

			// app name
			appname := ""
			if str, exist := pod.Labels[simontype.LabelAppName]; exist {
				appname = str
			}
			data := []string{
				node.Name,
				fmt.Sprintf("%s/%s", pod.Namespace, pod.Name),
				fmt.Sprintf("%s(%d%%)", cpuReq.String(), int64(fractionCpuReq)),
				fmt.Sprintf("%s(%d%%)", memoryReq.String(), int64(fractionMemoryReq)),
			}

			// Storage
			if containLocalStorage(extendedResources) {
				podVolumeStr := ""
				if volumes := utils.GetPodStorage(pod); volumes != nil {
					for i, volume := range volumes.Volumes {
						volumeQuantity := resource.NewQuantity(volume.Size, resource.BinarySI)
						volumeStr := fmt.Sprintf("<%d> %s: %s", i, volume.Kind, volumeQuantity.String())
						podVolumeStr = podVolumeStr + volumeStr
						if i+1 != len(volumes.Volumes) {
							podVolumeStr = fmt.Sprintf("%s\n", podVolumeStr)
						}
					}
				}
				data = append(data, podVolumeStr)
			}

			// GPU
			if containGpu(extendedResources) {
				gpuMem, gpuNum := gpushareutils.GetGpuMemoryAndCountFromPodAnnotation(pod)
				gpuMemReq := resource.NewQuantity(int64(gpuMem*gpuNum), resource.BinarySI)
				fractionGpuMemReq := float64(gpuMemReq.Value()) / float64(allocatable.Name(gpushareutils.ResourceName, resource.BinarySI).Value()) * 100
				data = append(data, fmt.Sprintf("%s(%d%%)", gpuMemReq.String(), int64(fractionGpuMemReq)))
			}

			data = append(data, appname)
			podTable.Append(data)
		}
	}
	podTable.SetAutoMergeCellsByColumnIndex([]int{0})
	podTable.SetRowLine(true)
	podTable.SetAlignment(tablewriter.ALIGN_LEFT)
	podTable.Render() // Send output

	fmt.Println()

	// Step 2: report node info
	fmt.Println("Node Info")
	nodeTable := tablewriter.NewWriter(os.Stdout)
	nodeTableHeader := []string{
		"Node",
		"CPU Allocatable",
		"CPU Requests",
		"Memory Allocatable",
		"Memory Requests",
	}
	if containGpu(extendedResources) {
		nodeTableHeader = append(nodeTableHeader, []string{
			"GPU Mem Allocatable",
			"GPU Mem Requests",
		}...)
	}
	nodeTableHeader = append(nodeTableHeader, []string{
		"Pod Count",
		"New Node",
	}...)
	nodeTable.SetHeader(nodeTableHeader)

	var allPods []corev1.Pod
	for _, status := range nodeStatuses {
		for _, pod := range status.Pods {
			allPods = append(allPods, *pod)
		}
	}
	for _, status := range nodeStatuses {
		node := status.Node
		allocatable := node.Status.Allocatable
		reqs, _ := utils.GetPodsTotalRequestsAndLimitsByNodeName(allPods, node.Name)
		nodeCpuReq, nodeMemoryReq := reqs[corev1.ResourceCPU], reqs[corev1.ResourceMemory]
		nodeCpuReqFraction := float64(nodeCpuReq.MilliValue()) / float64(allocatable.Cpu().MilliValue()) * 100
		nodeMemoryReqFraction := float64(nodeMemoryReq.Value()) / float64(allocatable.Memory().Value()) * 100
		newNode := ""
		if _, exist := node.Labels[simontype.LabelNewNode]; exist {
			newNode = "√"
		}

		data := []string{
			node.Name,
			allocatable.Cpu().String(),
			fmt.Sprintf("%s(%d%%)", nodeCpuReq.String(), int64(nodeCpuReqFraction)),
			allocatable.Memory().String(),
			fmt.Sprintf("%s(%d%%)", nodeMemoryReq.String(), int64(nodeMemoryReqFraction)),
		}
		if containGpu(extendedResources) {
			nodeGpuMemReq := resource.NewQuantity(0, resource.BinarySI)
			for _, pod := range allPods {
				if pod.Spec.NodeName == node.Name {
					gpuMem, gpuNum := gpushareutils.GetGpuMemoryAndCountFromPodAnnotation(&pod)
					gpuMemReq := resource.NewQuantity(int64(gpuMem*gpuNum), resource.BinarySI)
					nodeGpuMemReq.Add(*gpuMemReq)
				}
			}
			nodeGpuMemFraction := float64(nodeGpuMemReq.Value()) / float64(allocatable.Name(gpushareutils.ResourceName, resource.BinarySI).Value()) * 100
			data = append(data, []string{
				allocatable.Name(gpushareutils.ResourceName, resource.BinarySI).String(),
				fmt.Sprintf("%s(%d%%)", nodeGpuMemReq.String(), int64(nodeGpuMemFraction)),
			}...)
		}
		data = append(data, []string{
			fmt.Sprintf("%d", len(status.Pods)),
			newNode,
		}...)
		nodeTable.Append(data)
	}
	nodeTable.SetRowLine(true)
	nodeTable.SetAlignment(tablewriter.ALIGN_LEFT)
	nodeTable.Render() // Send output
	fmt.Println()

	// Step 3: report extended resource info (e.g., node storage, GPU)
	if len(extendedResources) != 0 {
		fmt.Println("Extended Resource Info")
		if containLocalStorage(extendedResources) {
			fmt.Println("Node Local Storage")
			nodeStorageTable := tablewriter.NewWriter(os.Stdout)
			nodeStorageTable.SetHeader([]string{
				"Node",
				"Storage Kind",
				"Storage Name",
				"Storage Allocatable",
				"Storage Requests",
			})
			for _, status := range nodeStatuses {
				node := status.Node
				if nodeStorageStr, exist := node.Annotations[simontype.AnnoNodeLocalStorage]; exist {
					var nodeStorage utils.NodeStorage
					if err := ffjson.Unmarshal([]byte(nodeStorageStr), &nodeStorage); err != nil {
						log.Fatalf("failed to unmarshal storage information of node(%s): %v", node.Name, err)
					}
					var storageData []string
					for _, vg := range nodeStorage.VGs {
						capacity := resource.NewQuantity(vg.Capacity, resource.BinarySI)
						request := resource.NewQuantity(vg.Requested, resource.BinarySI)
						storageData = []string{
							node.Name,
							"VG",
							vg.Name,
							capacity.String(),
							fmt.Sprintf("%s(%d%%)", request.String(), int64(float64(vg.Requested)/float64(vg.Capacity)*100)),
						}
						nodeStorageTable.Append(storageData)
					}

					for _, device := range nodeStorage.Devices {
						capacity := resource.NewQuantity(device.Capacity, resource.BinarySI)
						used := "unused"
						if device.IsAllocated {
							used = "used"
						}
						storageData = []string{
							node.Name,
							fmt.Sprintf("Device(%s)", device.MediaType),
							device.Device,
							capacity.String(),
							used,
						}
						nodeStorageTable.Append(storageData)
					}
				}
			}
			nodeStorageTable.SetAutoMergeCellsByColumnIndex([]int{0})
			nodeStorageTable.SetRowLine(true)
			nodeStorageTable.SetAlignment(tablewriter.ALIGN_LEFT)
			nodeStorageTable.Render() // Send output
		}
		if containGpu(extendedResources) {
			var podList []*corev1.Pod
			fmt.Println("GPU Node Resource")
			nodeGpuTable := tablewriter.NewWriter(os.Stdout)
			nodeGpuTable.SetHeader([]string{"Node", "GPU ID", "GPU Request/Capacity", "Pod List"})
			for _, status := range nodeStatuses {
				node := status.Node
				podList = append(podList, status.Pods...)
				if nodeGpuInfoStr, exist := node.Annotations[simontype.AnnoNodeGpuShare]; exist {
					var nodeGpuInfo gpusharecache.NodeGpuInfo
					if err := ffjson.Unmarshal([]byte(nodeGpuInfoStr), &nodeGpuInfo); err != nil {
						klog.Errorf("failed to unmarshal storage information of node(%s: %v", node.Name, err)
						continue
					}
					nodeGpuMemReq := resource.NewQuantity(0, resource.BinarySI)
					for _, pod := range allPods {
						if pod.Spec.NodeName == node.Name {
							gpuMem, gpuNum := gpushareutils.GetGpuMemoryAndCountFromPodAnnotation(&pod)
							gpuMemReq := resource.NewQuantity(int64(gpuMem*gpuNum), resource.BinarySI)
							nodeGpuMemReq.Add(*gpuMemReq)
						}
					}
					gpuReqCapFraction := float64(nodeGpuMemReq.Value())/float64(nodeGpuInfo.GpuTotalMemory.Value()) * 100
					gpuReqCapStr := fmt.Sprintf("%s/%s(%d%%)", nodeGpuMemReq.String(), nodeGpuInfo.GpuTotalMemory.String(), int(gpuReqCapFraction))
					nodeOutputLine := []string{fmt.Sprintf("%s (%s)", node.Name, nodeGpuInfo.GpuModel), fmt.Sprintf("%d GPUs", nodeGpuInfo.GpuCount), gpuReqCapStr, fmt.Sprintf("%d Pods", nodeGpuInfo.NumPods)}
					nodeGpuTable.Append(nodeOutputLine)

					for idx := 0; idx < len(nodeGpuInfo.DevsBrief); idx += 1 {
						if deviceInfoBrief, ok := nodeGpuInfo.DevsBrief[idx]; ok {
							devTotalGpuMem := deviceInfoBrief.GpuTotalMemory
							if devTotalGpuMem.Value() <= 0 {
								continue // either no GPU or not allocated
							}
							devUsedGpuMem := deviceInfoBrief.GpuUsedMemory
							devReqCapFraction := float64(devUsedGpuMem.Value())/float64(devTotalGpuMem.Value()) * 100
							devReqCapStr :=  fmt.Sprintf("%s/%s(%d%%)", devUsedGpuMem.String(), devTotalGpuMem.String(), int(devReqCapFraction))
							nodeOutputLineDev := []string{fmt.Sprintf("%s (%s)", node.Name, nodeGpuInfo.GpuModel), fmt.Sprintf("%d", idx), devReqCapStr, fmt.Sprintf("%s", deviceInfoBrief.PodList)}
							nodeGpuTable.Append(nodeOutputLineDev)
						}
					}
				}
			}
			nodeGpuTable.SetAutoMergeCellsByColumnIndex([]int{0})
			nodeGpuTable.SetRowLine(true)
			nodeGpuTable.SetAlignment(tablewriter.ALIGN_LEFT)
			nodeGpuTable.Render() // Send output

			fmt.Println("\nPod -> Node Map")
			podGpuTable := tablewriter.NewWriter(os.Stdout)
			podGpuTable.SetHeader([]string{"Pod", "CPU Req", "Mem Req", "GPU Req", "Host Node", "GPU IDX"})
			sort.Slice(podList, func(i, j int) bool { return podList[i].Name < podList[j].Name })
			for _, pod := range podList {
				req, limit := resourcehelper.PodRequestsAndLimits(pod)
				gpuMem, gpuNum := gpushareutils.GetGpuMemoryAndCountFromPodAnnotation(pod)
				gpuMemReq := resource.NewQuantity(int64(gpuMem*gpuNum), resource.BinarySI)
				cpuReq, _, memoryReq, _ := req[corev1.ResourceCPU], limit[corev1.ResourceCPU], req[corev1.ResourceMemory], limit[corev1.ResourceMemory]
				gpuIndex := gpushareutils.GetGpuIdFromAnnotation(pod)
				podOutputLine := []string{pod.Name, cpuReq.String(), memoryReq.String(), gpuMemReq.String(), pod.Spec.NodeName, gpuIndex}
				podGpuTable.Append(podOutputLine)
			}
			podGpuTable.SetRowLine(true)
			podGpuTable.SetAlignment(tablewriter.ALIGN_LEFT)
			podGpuTable.Render() // Send output
		}
	}
}

func satisfyResourceSetting(nodeStatuses []simulator.NodeStatus) (bool, string, error) {
	var err error
	var maxcpu int = 100
	var maxmem int = 100
	var maxvg int = 100
	if str := os.Getenv(simontype.EnvMaxCPU); str != "" {
		if maxcpu, err = strconv.Atoi(str); err != nil {
			return false, "", fmt.Errorf("failed to convert env %s to int: %s ", simontype.EnvMaxCPU, err.Error())
		}
		if maxcpu > 100 || maxcpu < 0 {
			maxcpu = 100
		}
	}

	if str := os.Getenv(simontype.EnvMaxMemory); str != "" {
		if maxmem, err = strconv.Atoi(str); err != nil {
			return false, "", fmt.Errorf("failed to convert env %s to int: %s ", simontype.EnvMaxMemory, err.Error())
		}
		if maxmem > 100 || maxmem < 0 {
			maxmem = 100
		}
	}

	if str := os.Getenv(simontype.EnvMaxVG); str != "" {
		if maxvg, err = strconv.Atoi(str); err != nil {
			return false, "", fmt.Errorf("failed to convert env %s to int: %s ", simontype.EnvMaxVG, err.Error())
		}
		if maxvg > 100 || maxvg < 0 {
			maxvg = 100
		}
	}

	totalAllocatableResource := map[corev1.ResourceName]*resource.Quantity{
		corev1.ResourceCPU:    resource.NewQuantity(0, resource.DecimalSI),
		corev1.ResourceMemory: resource.NewQuantity(0, resource.DecimalSI),
	}
	totalUsedResource := map[corev1.ResourceName]*resource.Quantity{
		corev1.ResourceCPU:    resource.NewQuantity(0, resource.DecimalSI),
		corev1.ResourceMemory: resource.NewQuantity(0, resource.DecimalSI),
	}
	totalVGResource := localcache.SharedResource{}
	var allPods []corev1.Pod
	for _, status := range nodeStatuses {
		for _, pod := range status.Pods {
			allPods = append(allPods, *pod)
		}
	}

	for _, status := range nodeStatuses {
		node := status.Node
		totalAllocatableResource[corev1.ResourceCPU].Add(*node.Status.Allocatable.Cpu())
		totalAllocatableResource[corev1.ResourceMemory].Add(*node.Status.Allocatable.Memory())

		reqs, _ := utils.GetPodsTotalRequestsAndLimitsByNodeName(allPods, node.Name)
		totalUsedResource[corev1.ResourceCPU].Add(reqs[corev1.ResourceCPU])
		totalUsedResource[corev1.ResourceMemory].Add(reqs[corev1.ResourceMemory])

		if nodeStorageStr, exist := node.Annotations[simontype.AnnoNodeLocalStorage]; exist {
			var nodeStorage utils.NodeStorage
			if err := ffjson.Unmarshal([]byte(nodeStorageStr), &nodeStorage); err != nil {
				return false, "", fmt.Errorf("error when unmarshal json data, node is %s\n", node.Name)
			}
			for _, vg := range nodeStorage.VGs {
				totalVGResource.Requested += vg.Requested
				totalVGResource.Capacity += vg.Capacity
			}
		}
	}

	cpuOccupancyRate := int(float64(totalUsedResource[corev1.ResourceCPU].MilliValue()) / float64(totalAllocatableResource[corev1.ResourceCPU].MilliValue()) * 100)
	memoryOccupancyRate := int(float64(totalUsedResource[corev1.ResourceMemory].MilliValue()) / float64(totalAllocatableResource[corev1.ResourceMemory].MilliValue()) * 100)
	if cpuOccupancyRate > maxcpu {
		return false, fmt.Sprintf("the average occupancy rate(%d%%) of cpu goes beyond the env setting(%d%%)\n", cpuOccupancyRate, maxcpu), nil
	}
	if memoryOccupancyRate > maxmem {
		return false, fmt.Sprintf("the average occupancy rate(%d%%) of memory goes beyond the env setting(%d%%)\n", memoryOccupancyRate, maxmem), nil
	}

	if totalVGResource.Capacity != 0 {
		vgOccupancyRate := int(float64(totalVGResource.Requested) / float64(totalVGResource.Capacity) * 100)
		if vgOccupancyRate > maxvg {
			return false, fmt.Sprintf("the average occupancy rate(%d%%) of vg goes beyond the env setting(%d%%)\n", vgOccupancyRate, maxvg), nil
		}
	}

	return true, "", nil
}

func containLocalStorage(extendedResources []string) bool {
	for _, res := range extendedResources {
		if res == "open-local" {
			return true
		}
	}
	return false
}

func containGpu(extendedResources []string) bool {
	for _, res := range extendedResources {
		if res == "gpu" {
			return true
		}
	}
	return false
}
