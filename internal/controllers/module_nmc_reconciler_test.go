package controllers

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	kmmv1beta1 "github.com/rh-ecosystem-edge/kernel-module-management/api/v1beta1"
	"github.com/rh-ecosystem-edge/kernel-module-management/internal/api"
	"github.com/rh-ecosystem-edge/kernel-module-management/internal/auth"
	"github.com/rh-ecosystem-edge/kernel-module-management/internal/client"
	"github.com/rh-ecosystem-edge/kernel-module-management/internal/constants"
	"github.com/rh-ecosystem-edge/kernel-module-management/internal/module"
	"github.com/rh-ecosystem-edge/kernel-module-management/internal/nmc"
	"github.com/rh-ecosystem-edge/kernel-module-management/internal/registry"
	"go.uber.org/mock/gomock"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Reconcile", func() {
	var (
		ctrl            *gomock.Controller
		mockReconHelper *MockmoduleNMCReconcilerHelperAPI
		mnr             *ModuleNMCReconciler
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockReconHelper = NewMockmoduleNMCReconcilerHelperAPI(ctrl)

		mnr = &ModuleNMCReconciler{
			reconHelper: mockReconHelper,
		}
	})

	const moduleName = "test-module"
	const nodeName = "nodeName"

	nsn := types.NamespacedName{
		Name:      moduleName,
		Namespace: namespace,
	}

	req := reconcile.Request{NamespacedName: nsn}

	ctx := context.Background()
	mod := kmmv1beta1.Module{}
	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
	}
	targetedNodes := []v1.Node{node}
	currentNMCs := sets.New[string](nodeName)
	mld := api.ModuleLoaderData{KernelVersion: "some version"}
	enableSchedulingData := schedulingData{mld: &mld, node: &node}
	disableSchedulingData := schedulingData{mld: nil, nmcExists: true}
	disableSchedulingDataNoNMC := schedulingData{mld: nil, nmcExists: false}

	It("should return ok if module has been deleted", func() {
		mockReconHelper.EXPECT().getRequestedModule(ctx, nsn).Return(nil, apierrors.NewNotFound(schema.GroupResource{}, "whatever"))

		res, err := mnr.Reconcile(ctx, req)

		Expect(res).To(Equal(reconcile.Result{}))
		Expect(err).NotTo(HaveOccurred())
	})

	It("should run finalization in case module is being deleted", func() {
		mod := kmmv1beta1.Module{}
		mod.SetDeletionTimestamp(&metav1.Time{})
		gomock.InOrder(
			mockReconHelper.EXPECT().getRequestedModule(ctx, nsn).Return(&mod, nil),
			mockReconHelper.EXPECT().finalizeModule(ctx, &mod).Return(nil),
		)

		res, err := mnr.Reconcile(ctx, req)

		Expect(res).To(Equal(reconcile.Result{}))
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("check error flows", func(getModuleError,
		setFinalizerError,
		getNodesError,
		getNMCsMapError,
		prepareSchedulingError,
		shouldBeOnNode bool) {

		nmcMLDConfigs := map[string]schedulingData{"nodeName": disableSchedulingData}
		if shouldBeOnNode {
			nmcMLDConfigs = map[string]schedulingData{"nodeName": enableSchedulingData}
		}
		returnedError := fmt.Errorf("some error")
		if getModuleError {
			mockReconHelper.EXPECT().getRequestedModule(ctx, nsn).Return(nil, returnedError)
			goto executeTestFunction
		}
		mockReconHelper.EXPECT().getRequestedModule(ctx, nsn).Return(&mod, nil)
		if setFinalizerError {
			mockReconHelper.EXPECT().setFinalizer(ctx, &mod).Return(returnedError)
			goto executeTestFunction
		}
		mockReconHelper.EXPECT().setFinalizer(ctx, &mod).Return(nil)
		if getNodesError {
			mockReconHelper.EXPECT().getNodesListBySelector(ctx, &mod).Return(nil, returnedError)
			goto executeTestFunction
		}
		mockReconHelper.EXPECT().getNodesListBySelector(ctx, &mod).Return(targetedNodes, nil)
		if getNMCsMapError {
			mockReconHelper.EXPECT().getNMCsByModuleSet(ctx, &mod).Return(nil, returnedError)
			goto executeTestFunction
		}
		mockReconHelper.EXPECT().getNMCsByModuleSet(ctx, &mod).Return(currentNMCs, nil)
		if prepareSchedulingError {
			mockReconHelper.EXPECT().prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs).Return(nil, []error{returnedError})
			goto executeTestFunction
		}
		mockReconHelper.EXPECT().prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs).Return(nmcMLDConfigs, []error{})
		if shouldBeOnNode {
			mockReconHelper.EXPECT().enableModuleOnNode(ctx, &mld, &node).Return(returnedError)
		} else {
			mockReconHelper.EXPECT().disableModuleOnNode(ctx, mod.Namespace, mod.Name, node.Name).Return(returnedError)
		}

	executeTestFunction:
		res, err := mnr.Reconcile(ctx, req)

		Expect(res).To(Equal(reconcile.Result{}))
		Expect(err).To(HaveOccurred())

	},
		Entry("getRequestedModule failed", true, false, false, false, false, false),
		Entry("setFinalizer failed", false, true, false, false, false, false),
		Entry("getNodesListBySelector failed", false, false, true, false, false, false),
		Entry("getNMCsByModuleMap failed", false, false, false, true, false, false),
		Entry("prepareSchedulingData failed", false, false, false, false, true, false),
		Entry("enableModuleOnNode failed", false, false, false, false, false, true),
		Entry("disableModuleOnNode failed", false, false, false, false, false, false),
	)

	It("Good flow, should run on node", func() {
		nmcMLDConfigs := map[string]schedulingData{nodeName: enableSchedulingData}
		gomock.InOrder(
			mockReconHelper.EXPECT().getRequestedModule(ctx, nsn).Return(&mod, nil),
			mockReconHelper.EXPECT().setFinalizer(ctx, &mod).Return(nil),
			mockReconHelper.EXPECT().getNodesListBySelector(ctx, &mod).Return(targetedNodes, nil),
			mockReconHelper.EXPECT().getNMCsByModuleSet(ctx, &mod).Return(currentNMCs, nil),
			mockReconHelper.EXPECT().prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs).Return(nmcMLDConfigs, nil),
			mockReconHelper.EXPECT().enableModuleOnNode(ctx, &mld, &node).Return(nil),
		)

		res, err := mnr.Reconcile(ctx, req)

		Expect(res).To(Equal(reconcile.Result{}))
		Expect(err).NotTo(HaveOccurred())
	})

	It("Good flow, should not run on node, nmc exists", func() {
		nmcMLDConfigs := map[string]schedulingData{nodeName: disableSchedulingData}
		gomock.InOrder(
			mockReconHelper.EXPECT().getRequestedModule(ctx, nsn).Return(&mod, nil),
			mockReconHelper.EXPECT().setFinalizer(ctx, &mod).Return(nil),
			mockReconHelper.EXPECT().getNodesListBySelector(ctx, &mod).Return(targetedNodes, nil),
			mockReconHelper.EXPECT().getNMCsByModuleSet(ctx, &mod).Return(currentNMCs, nil),
			mockReconHelper.EXPECT().prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs).Return(nmcMLDConfigs, nil),
			mockReconHelper.EXPECT().disableModuleOnNode(ctx, mod.Namespace, mod.Name, node.Name).Return(nil),
		)

		res, err := mnr.Reconcile(ctx, req)

		Expect(res).To(Equal(reconcile.Result{}))
		Expect(err).NotTo(HaveOccurred())
	})

	It("Good flow, should not run on node, nmc does not exist", func() {
		nmcMLDConfigs := map[string]schedulingData{nodeName: disableSchedulingDataNoNMC}
		gomock.InOrder(
			mockReconHelper.EXPECT().getRequestedModule(ctx, nsn).Return(&mod, nil),
			mockReconHelper.EXPECT().setFinalizer(ctx, &mod).Return(nil),
			mockReconHelper.EXPECT().getNodesListBySelector(ctx, &mod).Return(targetedNodes, nil),
			mockReconHelper.EXPECT().getNMCsByModuleSet(ctx, &mod).Return(currentNMCs, nil),
			mockReconHelper.EXPECT().prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs).Return(nmcMLDConfigs, nil),
		)

		res, err := mnr.Reconcile(ctx, req)

		Expect(res).To(Equal(reconcile.Result{}))
		Expect(err).NotTo(HaveOccurred())
	})

})

var _ = Describe("getRequestedModule", func() {
	var (
		ctrl *gomock.Controller
		clnt *client.MockClient
		mnrh moduleNMCReconcilerHelperAPI
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		clnt = client.NewMockClient(ctrl)
		mnrh = newModuleNMCReconcilerHelper(clnt, nil, nil, nil, nil, scheme)
	})

	ctx := context.Background()
	moduleName := "moduleName"
	moduleNamespace := "moduleNamespace"
	nsn := types.NamespacedName{Name: moduleName, Namespace: moduleNamespace}

	It("good flow", func() {
		clnt.EXPECT().Get(ctx, nsn, gomock.Any()).DoAndReturn(
			func(_ interface{}, _ interface{}, module *kmmv1beta1.Module, _ ...ctrlclient.GetOption) error {
				module.ObjectMeta = metav1.ObjectMeta{Name: moduleName, Namespace: moduleNamespace}
				return nil
			},
		)

		mod, err := mnrh.getRequestedModule(ctx, nsn)
		Expect(err).NotTo(HaveOccurred())
		Expect(mod.Name).To(Equal(moduleName))
		Expect(mod.Namespace).To(Equal(moduleNamespace))
	})

	It("error flow", func() {

		clnt.EXPECT().Get(ctx, nsn, gomock.Any()).Return(fmt.Errorf("some error"))
		mod, err := mnrh.getRequestedModule(ctx, nsn)
		Expect(err).To(HaveOccurred())
		Expect(mod).To(BeNil())
	})

})

var _ = Describe("setFinalizer", func() {
	var (
		ctrl *gomock.Controller
		clnt *client.MockClient
		mnrh moduleNMCReconcilerHelperAPI
		mod  kmmv1beta1.Module
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		clnt = client.NewMockClient(ctrl)
		mnrh = newModuleNMCReconcilerHelper(clnt, nil, nil, nil, nil, scheme)
		mod = kmmv1beta1.Module{}
	})

	ctx := context.Background()

	It("finalizer is already set", func() {
		controllerutil.AddFinalizer(&mod, constants.ModuleFinalizer)
		err := mnrh.setFinalizer(ctx, &mod)
		Expect(err).NotTo(HaveOccurred())
	})

	It("finalizer is not set", func() {
		clnt.EXPECT().Patch(ctx, &mod, gomock.Any()).Return(nil)

		err := mnrh.setFinalizer(ctx, &mod)
		Expect(err).NotTo(HaveOccurred())
	})

	It("finalizer is not set, failed to patch the Module", func() {
		clnt.EXPECT().Patch(ctx, &mod, gomock.Any()).Return(fmt.Errorf("some error"))

		err := mnrh.setFinalizer(ctx, &mod)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("getNodesListBySelector", func() {
	var (
		ctrl *gomock.Controller
		clnt *client.MockClient
		mnrh moduleNMCReconcilerHelperAPI
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		clnt = client.NewMockClient(ctrl)
		mnrh = newModuleNMCReconcilerHelper(clnt, nil, nil, nil, nil, scheme)
	})

	ctx := context.Background()

	It("list failed", func() {
		clnt.EXPECT().List(ctx, gomock.Any(), gomock.Any()).Return(fmt.Errorf("some error"))

		nodes, err := mnrh.getNodesListBySelector(ctx, &kmmv1beta1.Module{})

		Expect(err).To(HaveOccurred())
		Expect(nodes).To(BeNil())
	})

	It("Return nodes", func() {
		node1 := v1.Node{}
		node2 := v1.Node{}
		node3 := v1.Node{}
		clnt.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ interface{}, list *v1.NodeList, _ ...interface{}) error {
				list.Items = []v1.Node{node1, node2, node3}
				return nil
			},
		)
		nodes, err := mnrh.getNodesListBySelector(ctx, &kmmv1beta1.Module{})

		Expect(err).NotTo(HaveOccurred())
		Expect(nodes).To(Equal([]v1.Node{node1, node2, node3}))

	})
})

var _ = Describe("finalizeModule", func() {
	const (
		moduleName      = "moduleName"
		moduleNamespace = "moduleNamespace"
	)

	var (
		ctx                    context.Context
		ctrl                   *gomock.Controller
		clnt                   *client.MockClient
		helper                 *nmc.MockHelper
		mnrh                   moduleNMCReconcilerHelperAPI
		mod                    *kmmv1beta1.Module
		matchConfiguredModules = map[string]string{nmc.ModuleConfiguredLabel(moduleNamespace, moduleName): ""}
		matchLoadedModules     = map[string]string{nmc.ModuleInUseLabel(moduleNamespace, moduleName): ""}
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		clnt = client.NewMockClient(ctrl)
		helper = nmc.NewMockHelper(ctrl)
		mnrh = newModuleNMCReconcilerHelper(clnt, nil, nil, helper, nil, scheme)
		mod = &kmmv1beta1.Module{
			ObjectMeta: metav1.ObjectMeta{Name: moduleName, Namespace: moduleNamespace},
		}
	})

	It("failed to get list of NMCs", func() {
		clnt.
			EXPECT().
			List(ctx, &kmmv1beta1.NodeModulesConfigList{}, matchConfiguredModules).
			Return(fmt.Errorf("some error"))

		err := mnrh.finalizeModule(ctx, mod)

		Expect(err).To(HaveOccurred())
	})

	It("multiple errors occurred", func() {
		nmc1 := kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "nmc1"},
		}
		nmc2 := kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "nmc2"},
		}

		gomock.InOrder(
			clnt.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ interface{}, list *kmmv1beta1.NodeModulesConfigList, _ ...interface{}) error {
					list.Items = []kmmv1beta1.NodeModulesConfig{nmc1, nmc2}
					return nil
				},
			),
			clnt.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(fmt.Errorf("some error")),
			clnt.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(fmt.Errorf("some error")),
		)

		err := mnrh.finalizeModule(ctx, mod)

		Expect(err).To(HaveOccurred())

	})

	It("no nmcs, patch successfull", func() {
		gomock.InOrder(
			clnt.EXPECT().List(ctx, &kmmv1beta1.NodeModulesConfigList{}, matchConfiguredModules).DoAndReturn(
				func(_ interface{}, list *kmmv1beta1.NodeModulesConfigList, _ ...interface{}) error {
					list.Items = []kmmv1beta1.NodeModulesConfig{}
					return nil
				},
			),
			clnt.EXPECT().List(ctx, &kmmv1beta1.NodeModulesConfigList{}, matchLoadedModules),
			clnt.EXPECT().Patch(ctx, mod, gomock.Any()).Return(nil),
		)

		err := mnrh.finalizeModule(ctx, mod)

		Expect(err).NotTo(HaveOccurred())
	})

	It("some nmcs have the Module loaded, does not patch", func() {
		gomock.InOrder(
			clnt.EXPECT().List(ctx, &kmmv1beta1.NodeModulesConfigList{}, matchConfiguredModules).DoAndReturn(
				func(_ interface{}, list *kmmv1beta1.NodeModulesConfigList, _ ...interface{}) error {
					list.Items = make([]kmmv1beta1.NodeModulesConfig, 0)
					return nil
				},
			),
			clnt.EXPECT().List(ctx, &kmmv1beta1.NodeModulesConfigList{}, matchLoadedModules).DoAndReturn(
				func(_ interface{}, list *kmmv1beta1.NodeModulesConfigList, _ ...interface{}) error {
					list.Items = make([]kmmv1beta1.NodeModulesConfig, 1)
					return nil
				},
			),
		)

		Expect(
			mnrh.finalizeModule(ctx, mod),
		).NotTo(
			HaveOccurred(),
		)
	})

	It("no nmcs, patch failed", func() {
		gomock.InOrder(
			clnt.EXPECT().List(ctx, &kmmv1beta1.NodeModulesConfigList{}, matchConfiguredModules).DoAndReturn(
				func(_ interface{}, list *kmmv1beta1.NodeModulesConfigList, _ ...interface{}) error {
					list.Items = []kmmv1beta1.NodeModulesConfig{}
					return nil
				},
			),
			clnt.EXPECT().List(ctx, &kmmv1beta1.NodeModulesConfigList{}, matchLoadedModules),
			clnt.EXPECT().Patch(ctx, mod, gomock.Any()).Return(fmt.Errorf("some error")),
		)

		err := mnrh.finalizeModule(ctx, mod)

		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("getNMCsByModuleSet", func() {
	var (
		ctrl *gomock.Controller
		clnt *client.MockClient
		mnrh moduleNMCReconcilerHelperAPI
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		clnt = client.NewMockClient(ctrl)
		mnrh = newModuleNMCReconcilerHelper(clnt, nil, nil, nil, nil, scheme)
	})

	ctx := context.Background()

	It("list failed", func() {
		clnt.EXPECT().List(ctx, gomock.Any(), gomock.Any()).Return(fmt.Errorf("some error"))

		nodes, err := mnrh.getNMCsByModuleSet(ctx, &kmmv1beta1.Module{})

		Expect(err).To(HaveOccurred())
		Expect(nodes).To(BeNil())
	})

	It("Return NMCs", func() {
		nmc1 := kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "nmc1"},
		}
		nmc2 := kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "nmc2"},
		}
		nmc3 := kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "nmc3"},
		}
		clnt.EXPECT().List(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ interface{}, list *kmmv1beta1.NodeModulesConfigList, _ ...interface{}) error {
				list.Items = []kmmv1beta1.NodeModulesConfig{nmc1, nmc2, nmc3}
				return nil
			},
		)

		nmcsSet, err := mnrh.getNMCsByModuleSet(ctx, &kmmv1beta1.Module{})

		expectedSet := sets.New[string]([]string{"nmc1", "nmc2", "nmc3"}...)

		Expect(err).NotTo(HaveOccurred())
		Expect(nmcsSet.Equal(expectedSet)).To(BeTrue())

	})
})

var _ = Describe("prepareSchedulingData", func() {
	var (
		ctrl       *gomock.Controller
		clnt       *client.MockClient
		mockKernel *module.MockKernelMapper
		mockHelper *nmc.MockHelper
		mnrh       moduleNMCReconcilerHelperAPI
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		clnt = client.NewMockClient(ctrl)
		mockKernel = module.NewMockKernelMapper(ctrl)
		mockHelper = nmc.NewMockHelper(ctrl)
		mnrh = newModuleNMCReconcilerHelper(clnt, mockKernel, nil, mockHelper, nil, scheme)
	})

	const kernelVersion = "some kernel version"
	const nodeName = "nodeName"

	ctx := context.Background()
	mod := kmmv1beta1.Module{}
	node := v1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: v1.NodeStatus{
			NodeInfo: v1.NodeSystemInfo{KernelVersion: kernelVersion},
		},
	}
	targetedNodes := []v1.Node{node}
	mld := api.ModuleLoaderData{KernelVersion: "some version"}

	It("failed to determine mld", func() {
		currentNMCs := sets.New[string](nodeName)
		mockKernel.EXPECT().GetModuleLoaderDataForKernel(&mod, kernelVersion).Return(nil, fmt.Errorf("some error"))

		scheduleData, errs := mnrh.prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs)

		Expect(len(errs)).To(Equal(1))
		Expect(scheduleData).To(Equal(map[string]schedulingData{}))
	})

	It("mld for kernel version does not exists", func() {
		currentNMCs := sets.New[string](nodeName)
		mockKernel.EXPECT().GetModuleLoaderDataForKernel(&mod, kernelVersion).Return(nil, module.ErrNoMatchingKernelMapping)

		scheduleData, errs := mnrh.prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs)

		expectedScheduleData := map[string]schedulingData{nodeName: schedulingData{mld: nil, node: &node, nmcExists: true}}
		Expect(errs).To(BeEmpty())
		Expect(scheduleData).To(Equal(expectedScheduleData))
	})

	It("mld exists", func() {
		currentNMCs := sets.New[string](nodeName)
		mockKernel.EXPECT().GetModuleLoaderDataForKernel(&mod, kernelVersion).Return(&mld, nil)

		scheduleData, errs := mnrh.prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs)

		expectedScheduleData := map[string]schedulingData{nodeName: schedulingData{mld: &mld, node: &node, nmcExists: true}}
		Expect(errs).To(BeEmpty())
		Expect(scheduleData).To(Equal(expectedScheduleData))
	})

	It("mld exists, nmc exists for other node", func() {
		currentNMCs := sets.New[string]("some other node")
		mockKernel.EXPECT().GetModuleLoaderDataForKernel(&mod, kernelVersion).Return(&mld, nil)

		scheduleData, errs := mnrh.prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs)

		Expect(errs).To(BeEmpty())
		Expect(scheduleData).To(HaveKeyWithValue(nodeName, schedulingData{mld: &mld, node: &node, nmcExists: false}))
		Expect(scheduleData).To(HaveKeyWithValue("some other node", schedulingData{mld: nil, nmcExists: true}))
	})

	It("failed to determine mld for one of the nodes/nmcs", func() {
		currentNMCs := sets.New[string]("some other node")
		mockKernel.EXPECT().GetModuleLoaderDataForKernel(&mod, kernelVersion).Return(nil, fmt.Errorf("some error"))

		scheduleData, errs := mnrh.prepareSchedulingData(ctx, &mod, targetedNodes, currentNMCs)

		Expect(errs).NotTo(BeEmpty())
		expectedScheduleData := map[string]schedulingData{"some other node": schedulingData{mld: nil, nmcExists: true}}
		Expect(scheduleData).To(Equal(expectedScheduleData))
	})
})

var _ = Describe("enableModuleOnNode", func() {
	const (
		moduleNamespace = "moduleNamespace"
		moduleName      = "moduleName"
	)

	var (
		ctx                  context.Context
		ctrl                 *gomock.Controller
		clnt                 *client.MockClient
		rgst                 *registry.MockRegistry
		authFactory          *auth.MockRegistryAuthGetterFactory
		mnrh                 moduleNMCReconcilerHelperAPI
		helper               *nmc.MockHelper
		mld                  *api.ModuleLoaderData
		node                 v1.Node
		expectedModuleConfig *kmmv1beta1.ModuleConfig
		kernelVersion        string
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		clnt = client.NewMockClient(ctrl)
		helper = nmc.NewMockHelper(ctrl)
		rgst = registry.NewMockRegistry(ctrl)
		authFactory = auth.NewMockRegistryAuthGetterFactory(ctrl)
		mnrh = newModuleNMCReconcilerHelper(clnt, nil, rgst, helper, authFactory, scheme)
		node = v1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "nodeName"},
		}
		kernelVersion = "some version"
		ctx = context.Background()
		mld = &api.ModuleLoaderData{
			KernelVersion:        kernelVersion,
			Name:                 moduleName,
			Namespace:            moduleNamespace,
			InTreeModuleToRemove: "InTreeModuleToRemove",
			ContainerImage:       "containerImage",
		}

		expectedModuleConfig = &kmmv1beta1.ModuleConfig{
			KernelVersion:        mld.KernelVersion,
			ContainerImage:       mld.ContainerImage,
			InTreeModuleToRemove: mld.InTreeModuleToRemove,
			Modprobe:             mld.Modprobe,
		}
	})

	It("Image does not exists", func() {
		authGetter := &auth.MockRegistryAuthGetter{}
		gomock.InOrder(
			authFactory.EXPECT().NewRegistryAuthGetterFrom(mld).Return(authGetter),
			rgst.EXPECT().ImageExists(ctx, mld.ContainerImage, gomock.Any(), authGetter).Return(false, nil),
		)
		err := mnrh.enableModuleOnNode(ctx, mld, &node)
		Expect(err).NotTo(HaveOccurred())
	})

	It("Failed to check if image exists", func() {
		authGetter := &auth.MockRegistryAuthGetter{}
		gomock.InOrder(
			authFactory.EXPECT().NewRegistryAuthGetterFrom(mld).Return(authGetter),
			rgst.EXPECT().ImageExists(ctx, mld.ContainerImage, gomock.Any(), authGetter).Return(false, fmt.Errorf("some error")),
		)
		err := mnrh.enableModuleOnNode(ctx, mld, &node)
		Expect(err).To(HaveOccurred())
	})

	It("NMC does not exists", func() {
		nmc := &kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: node.Name},
		}

		authGetter := &auth.MockRegistryAuthGetter{}
		gomock.InOrder(
			authFactory.EXPECT().NewRegistryAuthGetterFrom(mld).Return(authGetter),
			rgst.EXPECT().ImageExists(ctx, mld.ContainerImage, gomock.Any(), authGetter).Return(true, nil),
			clnt.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).Return(apierrors.NewNotFound(schema.GroupResource{}, "whatever")),
			helper.EXPECT().SetModuleConfig(nmc, mld, expectedModuleConfig).Return(nil),
			clnt.EXPECT().Create(ctx, gomock.Any()).Return(nil),
		)

		err := mnrh.enableModuleOnNode(ctx, mld, &node)
		Expect(err).NotTo(HaveOccurred())
	})

	It("NMC exists", func() {
		nmcObj := &kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: node.Name},
		}

		authGetter := &auth.MockRegistryAuthGetter{}

		nmcWithLabels := *nmcObj
		nmcWithLabels.SetLabels(map[string]string{
			nmc.ModuleConfiguredLabel(moduleNamespace, moduleName): "",
			nmc.ModuleInUseLabel(moduleNamespace, moduleName):      "",
		})

		Expect(
			controllerutil.SetOwnerReference(&node, &nmcWithLabels, scheme),
		).NotTo(
			HaveOccurred(),
		)

		gomock.InOrder(
			authFactory.EXPECT().NewRegistryAuthGetterFrom(mld).Return(authGetter),
			rgst.EXPECT().ImageExists(ctx, mld.ContainerImage, gomock.Any(), authGetter).Return(true, nil),
			clnt.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ interface{}, _ interface{}, nmc *kmmv1beta1.NodeModulesConfig, _ ...ctrlclient.GetOption) error {
					nmc.SetName(node.Name)
					return nil
				},
			),
			helper.EXPECT().SetModuleConfig(nmcObj, mld, expectedModuleConfig).Return(nil),
			clnt.EXPECT().Patch(ctx, &nmcWithLabels, gomock.Any()).Return(nil),
		)

		err := mnrh.enableModuleOnNode(ctx, mld, &node)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("disableModuleOnNode", func() {
	var (
		ctx             context.Context
		ctrl            *gomock.Controller
		clnt            *client.MockClient
		mnrh            moduleNMCReconcilerHelperAPI
		helper          *nmc.MockHelper
		nodeName        string
		moduleName      string
		moduleNamespace string
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		clnt = client.NewMockClient(ctrl)
		helper = nmc.NewMockHelper(ctrl)
		mnrh = newModuleNMCReconcilerHelper(clnt, nil, nil, helper, nil, scheme)
		nodeName = "node name"
		moduleName = "moduleName"
		moduleNamespace = "moduleNamespace"
	})

	It("NMC exists", func() {
		nmc := &kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		}
		gomock.InOrder(
			clnt.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ interface{}, _ interface{}, nmc *kmmv1beta1.NodeModulesConfig, _ ...ctrlclient.GetOption) error {
					nmc.SetName(nodeName)
					return nil
				},
			),
			helper.EXPECT().RemoveModuleConfig(nmc, moduleNamespace, moduleName).Return(nil),
		)

		err := mnrh.disableModuleOnNode(ctx, moduleNamespace, moduleName, nodeName)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("removeModuleFromNMC", func() {
	var (
		ctx             context.Context
		ctrl            *gomock.Controller
		clnt            *client.MockClient
		mnrh            *moduleNMCReconcilerHelper
		helper          *nmc.MockHelper
		nmcName         string
		moduleName      string
		moduleNamespace string
	)

	BeforeEach(func() {
		ctx = context.Background()
		ctrl = gomock.NewController(GinkgoT())
		clnt = client.NewMockClient(ctrl)
		helper = nmc.NewMockHelper(ctrl)
		mnrh = &moduleNMCReconcilerHelper{client: clnt, nmcHelper: helper}
		nmcName = "NMC name"
		moduleName = "moduleName"
		moduleNamespace = "moduleNamespace"
	})

	It("good flow", func() {
		nmc := &kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: nmcName},
		}
		gomock.InOrder(
			clnt.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ interface{}, _ interface{}, nmc *kmmv1beta1.NodeModulesConfig, _ ...ctrlclient.GetOption) error {
					nmc.SetName(nmcName)
					return nil
				},
			),
			helper.EXPECT().RemoveModuleConfig(nmc, moduleNamespace, moduleName).Return(nil),
		)

		err := mnrh.removeModuleFromNMC(ctx, nmc, moduleNamespace, moduleName)
		Expect(err).NotTo(HaveOccurred())
	})

	It("bad flow", func() {
		nmc := &kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{Name: nmcName},
		}
		gomock.InOrder(
			clnt.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ interface{}, _ interface{}, nmc *kmmv1beta1.NodeModulesConfig, _ ...ctrlclient.GetOption) error {
					nmc.SetName(nmcName)
					return nil
				},
			),
			helper.EXPECT().RemoveModuleConfig(nmc, moduleNamespace, moduleName).Return(fmt.Errorf("some error")),
		)

		err := mnrh.removeModuleFromNMC(ctx, nmc, moduleNamespace, moduleName)
		Expect(err).To(HaveOccurred())
	})

	It("removes the configured label", func() {
		nmc := &kmmv1beta1.NodeModulesConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:   nmcName,
				Labels: map[string]string{nmc.ModuleConfiguredLabel(moduleNamespace, moduleName): ""},
			},
		}

		nmcWithoutLabel := *nmc
		nmcWithoutLabel.SetLabels(make(map[string]string))

		gomock.InOrder(
			clnt.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ interface{}, _ interface{}, nmc *kmmv1beta1.NodeModulesConfig, _ ...ctrlclient.GetOption) error {
					nmc.SetName(nmcName)
					return nil
				},
			),
			helper.EXPECT().RemoveModuleConfig(nmc, moduleNamespace, moduleName).Return(nil),
			clnt.EXPECT().Patch(ctx, &nmcWithoutLabel, gomock.Any()),
		)

		err := mnrh.removeModuleFromNMC(ctx, nmc, moduleNamespace, moduleName)
		Expect(err).NotTo(HaveOccurred())
	})
})