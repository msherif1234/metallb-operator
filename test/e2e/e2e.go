package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	metallbv1alpha1 "github.com/metallb/metallb-operator/api/v1alpha1"
	"github.com/metallb/metallb-operator/test/consts"
	testclient "github.com/metallb/metallb-operator/test/e2e/client"
	corev1 "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	goclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
)

const (
	// Timeout and Interval settings
	timeout  = time.Minute * 3
	interval = time.Second * 2
)

var autoAssign = false

func RunE2ETests(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = Describe("validation", func() {
	Context("MetalLB", func() {
		It("should have the MetalLB operator deployment in running state", func() {
			Eventually(func() bool {
				deploy, err := testclient.Client.Deployments(consts.MetallbNameSpace).Get(context.Background(), consts.MetallbOperatorDeploymentName, metav1.GetOptions{})
				if err != nil {
					return false
				}
				return deploy.Status.ReadyReplicas == deploy.Status.Replicas
			}, timeout, interval).Should(BeTrue())

			pods, err := testclient.Client.Pods(consts.MetallbNameSpace).List(context.Background(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("control-plane=%s", consts.MetallbOperatorDeploymentLabel)})
			Expect(err).ToNot(HaveOccurred())

			deploy, err := testclient.Client.Deployments(consts.MetallbNameSpace).Get(context.Background(), consts.MetallbOperatorDeploymentName, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(len(pods.Items)).To(Equal(int(deploy.Status.Replicas)))

			for _, pod := range pods.Items {
				Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
			}
		})

		It("should have the MetalLB CRD available in the cluster", func() {
			crd := &apiext.CustomResourceDefinition{}
			err := testclient.Client.Get(context.Background(), goclient.ObjectKey{Name: consts.MetallbOperatorCRDName}, crd)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should have the MetalLB AddressPool CRD available in the cluster", func() {
			crd := &apiext.CustomResourceDefinition{}
			err := testclient.Client.Get(context.Background(), goclient.ObjectKey{Name: consts.MetallbAddressPoolCRDName}, crd)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("MetalLB deploy", func() {
		var metallb *metallbv1alpha1.Metallb
		var metallbCRExisted bool

		BeforeEach(func() {
			metallb = &metallbv1alpha1.Metallb{}
			err := loadMetallbFromFile(metallb, consts.MetallbCRFile)
			Expect(err).ToNot(HaveOccurred())

			metallbCRExisted = true
			err = testclient.Client.Get(context.Background(), goclient.ObjectKey{Namespace: metallb.Namespace, Name: metallb.Name}, metallb)
			if errors.IsNotFound(err) {
				metallbCRExisted = false
				Expect(testclient.Client.Create(context.Background(), metallb)).Should(Succeed())
			} else {
				Expect(err).ToNot(HaveOccurred())
			}
		})

		AfterEach(func() {
			if !metallbCRExisted {
				deployment, err := testclient.Client.Deployments(consts.MetallbNameSpace).Get(context.Background(), consts.MetallbDeploymentName, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				Expect(deployment.OwnerReferences).ToNot(BeNil())
				Expect(deployment.OwnerReferences[0].Kind).To(Equal("Metallb"))

				daemonset, err := testclient.Client.DaemonSets(metallb.Namespace).Get(context.Background(), consts.MetallbDaemonsetName, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				Expect(daemonset.OwnerReferences).ToNot(BeNil())
				Expect(daemonset.OwnerReferences[0].Kind).To(Equal("Metallb"))

				err = testclient.Client.Delete(context.Background(), metallb)
				Expect(err).ToNot(HaveOccurred())
				// Check the MetalLB custom resource is deleted to avoid status leak in between tests.
				Eventually(func() bool {
					err = testclient.Client.Get(context.Background(), goclient.ObjectKey{Namespace: metallb.Namespace, Name: metallb.Name}, metallb)
					return errors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue(), "Failed to delete MetalLB custom resource")

				Eventually(func() bool {
					_, err := testclient.Client.Deployments(metallb.Namespace).Get(context.Background(), consts.MetallbDeploymentName, metav1.GetOptions{})
					return errors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())

				Eventually(func() bool {
					_, err := testclient.Client.DaemonSets(metallb.Namespace).Get(context.Background(), consts.MetallbDaemonsetName, metav1.GetOptions{})
					return errors.IsNotFound(err)
				}, timeout, interval).Should(BeTrue())
			}
		})

		It("should have MetalLB pods in running state", func() {
			By("checking MetalLB controller deployment is in running state", func() {
				Eventually(func() bool {
					deploy, err := testclient.Client.Deployments(metallb.Namespace).Get(context.Background(), consts.MetallbDeploymentName, metav1.GetOptions{})
					if err != nil {
						return false
					}
					return deploy.Status.ReadyReplicas == deploy.Status.Replicas
				}, timeout, interval).Should(BeTrue())

				pods, err := testclient.Client.Pods(consts.MetallbNameSpace).List(context.Background(), metav1.ListOptions{
					LabelSelector: "component=controller"})
				Expect(err).ToNot(HaveOccurred())

				deploy, err := testclient.Client.Deployments(metallb.Namespace).Get(context.Background(), consts.MetallbDeploymentName, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(pods.Items)).To(Equal(int(deploy.Status.Replicas)))

				for _, pod := range pods.Items {
					Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
				}
			})

			By("checking MetalLB daemonset is in running state", func() {
				Eventually(func() bool {
					daemonset, err := testclient.Client.DaemonSets(metallb.Namespace).Get(context.Background(), consts.MetallbDaemonsetName, metav1.GetOptions{})
					if err != nil {
						return false
					}
					return daemonset.Status.DesiredNumberScheduled == daemonset.Status.NumberReady
				}, timeout, interval).Should(BeTrue())

				pods, err := testclient.Client.Pods(consts.MetallbNameSpace).List(context.Background(), metav1.ListOptions{
					LabelSelector: "component=speaker"})
				Expect(err).ToNot(HaveOccurred())

				daemonset, err := testclient.Client.DaemonSets(metallb.Namespace).Get(context.Background(), consts.MetallbDaemonsetName, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				Expect(len(pods.Items)).To(Equal(int(daemonset.Status.DesiredNumberScheduled)))

				for _, pod := range pods.Items {
					Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
				}
			})
		})
	})

	Context("Creating AddressPool", func() {
		table.DescribeTable("Testing creating addresspool CR successfully", func(addressPoolName string, addresspool *metallbv1alpha1.AddressPool, expectedConfigMap string) {
			By("By creating AddressPool CR")

			Expect(testclient.Client.Create(context.Background(), addresspool)).Should(Succeed())

			key := types.NamespacedName{
				Name:      addressPoolName,
				Namespace: consts.MetallbNameSpace,
			}
			// Create addresspool resource
			By("By checking AddressPool resource is created")
			Eventually(func() error {
				err := testclient.Client.Get(context.Background(), key, addresspool)
				return err
			}, timeout, interval).Should(Succeed())

			// Checking ConfigMap is created
			By("By checking ConfigMap is created match the expected configuration")
			Eventually(func() (string, error) {
				configmap, err := testclient.Client.ConfigMaps(consts.MetallbNameSpace).Get(context.Background(), consts.MetallbConfigMapName, metav1.GetOptions{})
				if err != nil {
					return "", err
				}
				return configmap.Data[consts.MetallbConfigMapName], err
			}, timeout, interval).Should(MatchYAML(expectedConfigMap))

			By("By checking AddressPool resource and ConfigMap are deleted")
			Eventually(func() bool {
				err := testclient.Client.Delete(context.Background(), addresspool)
				return errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue(), "Failed to delete AddressPool custom resource")

			Eventually(func() bool {
				_, err := testclient.Client.ConfigMaps(consts.MetallbNameSpace).Get(context.Background(), consts.MetallbConfigMapName, metav1.GetOptions{})
				return errors.IsNotFound(err)
			}, timeout, interval).Should(BeTrue())
		},
			table.Entry("Test AddressPool object with default auto assign", "addresspool1", &metallbv1alpha1.AddressPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "addresspool1",
					Namespace: consts.MetallbNameSpace,
				},
				Spec: metallbv1alpha1.AddressPoolSpec{
					Name:     "test1",
					Protocol: "layer2",
					Addresses: []string{
						"1.1.1.1",
						"1.1.1.100",
					},
				},
			}, `address-pools:
- name: test1
  protocol: layer2
  addresses:

  - 1.1.1.1
  - 1.1.1.100

`),
			table.Entry("Test AddressPool object with auto assign set to false", "addresspool2", &metallbv1alpha1.AddressPool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "addresspool2",
					Namespace: consts.MetallbNameSpace,
				},
				Spec: metallbv1alpha1.AddressPoolSpec{
					Name:     "test2",
					Protocol: "layer2",
					Addresses: []string{
						"2.2.2.1",
						"2.2.2.100",
					},
					AutoAssign: &autoAssign,
				},
			}, `address-pools:
- name: test2
  protocol: layer2
  auto-assign: false
  addresses:

  - 2.2.2.1
  - 2.2.2.100

`))
	})
})

func decodeYAML(r io.Reader, obj interface{}) error {
	decoder := yaml.NewYAMLToJSONDecoder(r)
	return decoder.Decode(obj)
}

func loadMetallbFromFile(metallb *metallbv1alpha1.Metallb, fileName string) error {
	f, err := os.Open(fmt.Sprintf("../../config/samples/%s", fileName))
	if err != nil {
		return err
	}
	defer f.Close()

	return decodeYAML(f, metallb)
}

var _ = BeforeSuite(func() {
	_, err := testclient.Client.Namespaces().Get(context.Background(), consts.MetallbNameSpace, metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred(), "Should have the MetalLB operator namespace")
})