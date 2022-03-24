//go:build e2e
// +build e2e

package e2e

import (
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1apply "k8s.io/client-go/applyconfigurations/apps/v1"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	v1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	networkingv1apply "k8s.io/client-go/applyconfigurations/networking/v1"

	clusterv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/cluster/v1alpha1"

	. "github.com/kuadrant/kcp-glbc/e2e/support"
	kuadrantv1 "github.com/kuadrant/kcp-glbc/pkg/apis/kuadrant/v1"
	kuadrantcluster "github.com/kuadrant/kcp-glbc/pkg/cluster"
)

var applyOptions = metav1.ApplyOptions{FieldManager: "kcp-glbc-e2e", Force: true}

func TestIngress(t *testing.T) {
	test := With(t)

	// Create the test workspace
	workspace := test.NewTestWorkspace()

	// Register workload cluster 1 into the test workspace
	cluster1 := test.NewWorkloadCluster("kcp-cluster-1", WithKubeConfigByName, InWorkspace(workspace))

	// Wait until cluster 1 is ready
	test.Eventually(WorkloadCluster(test, cluster1.ClusterName, cluster1.Name)).Should(WithTransform(
		ConditionStatus(clusterv1alpha1.ClusterReadyCondition),
		Equal(corev1.ConditionTrue),
	))

	// Wait until the APIs are imported into the workspace
	test.Eventually(HasImportedAPIs(test, workspace,
		corev1.SchemeGroupVersion.WithKind("Service"),
		appsv1.SchemeGroupVersion.WithKind("Deployment"),
		networkingv1.SchemeGroupVersion.WithKind("Ingress"),
	)).Should(BeTrue())

	// Create a namespace with automatic scheduling disabled
	namespace := test.NewTestNamespace(InWorkspace(workspace), WithLabel("experimental.scheduling.kcp.dev/disabled", ""))

	name := "echo"

	// Create the Deployment and Service for cluster 1
	syncToCluster1 := map[string]string{ClusterLabel: cluster1.Name}

	_, err := test.Client().Core().Cluster(namespace.ClusterName).AppsV1().Deployments(namespace.Name).
		Apply(test.Ctx(), deploymentConfiguration(namespace.Name, name+"1", syncToCluster1), applyOptions)
	test.Expect(err).NotTo(HaveOccurred())

	_, err = test.Client().Core().Cluster(namespace.ClusterName).CoreV1().Services(namespace.Name).
		Apply(test.Ctx(), serviceConfiguration(namespace.Name, name+"1", syncToCluster1), applyOptions)
	test.Expect(err).NotTo(HaveOccurred())

	// Create the root Ingress
	_, err = test.Client().Core().Cluster(namespace.ClusterName).NetworkingV1().Ingresses(namespace.Name).
		Apply(test.Ctx(), ingressConfiguration(namespace.Name, name, name+"1"), applyOptions)
	test.Expect(err).NotTo(HaveOccurred())

	// Wait until the root Ingress is reconciled with the load balancer Ingresses
	test.Eventually(Ingress(test, namespace, name)).WithTimeout(TestTimeoutMedium).Should(And(
		WithTransform(Annotations, And(
			HaveKey(kuadrantcluster.ANNOTATION_HCG_HOST),
			HaveKey(kuadrantcluster.ANNOTATION_HCG_CUSTOM_HOST_REPLACED)),
		),
		WithTransform(LoadBalancerIngresses, HaveLen(1)),
	))

	// Retrieve the root Ingress
	ingress := GetIngress(test, namespace, name)

	// Check the host value is being replaced with the generated host
	test.Expect(ingress.Spec.Rules).To(MatchFields(IgnoreExtras,
		Fields{
			"Hosts": Equal(ingress.Annotations[kuadrantcluster.ANNOTATION_HCG_HOST]),
		}),
	)

	// Check a DNSRecord for the root Ingress is created with the expected Spec
	test.Eventually(DNSRecord(test, namespace, name)).Should(PointTo(MatchFields(IgnoreExtras, Fields{
		"Spec": MatchFields(IgnoreExtras,
			Fields{
				"DNSName":    Equal(ingress.Annotations[kuadrantcluster.ANNOTATION_HCG_HOST]),
				"Targets":    ConsistOf(ingress.Status.LoadBalancer.Ingress[0].IP),
				"RecordType": Equal(kuadrantv1.ARecordType),
				"RecordTTL":  Equal(int64(60)),
			}),
	})))

	// Register workload cluster 2 into the test workspace
	cluster2 := test.NewWorkloadCluster("kcp-cluster-2", WithKubeConfigByName, InWorkspace(workspace))

	// Wait until cluster 2 is ready
	test.Eventually(WorkloadCluster(test, cluster1.ClusterName, cluster1.Name)).Should(WithTransform(
		ConditionStatus(clusterv1alpha1.ClusterReadyCondition),
		Equal(corev1.ConditionTrue),
	))

	// Create the Deployment and Service for cluster 2
	syncToCluster2 := map[string]string{ClusterLabel: cluster2.Name}

	_, err = test.Client().Core().Cluster(namespace.ClusterName).AppsV1().Deployments(namespace.Name).
		Apply(test.Ctx(), deploymentConfiguration(namespace.Name, name+"2", syncToCluster2), applyOptions)
	test.Expect(err).NotTo(HaveOccurred())

	_, err = test.Client().Core().Cluster(namespace.ClusterName).CoreV1().Services(namespace.Name).
		Apply(test.Ctx(), serviceConfiguration(namespace.Name, name+"2", syncToCluster2), applyOptions)
	test.Expect(err).NotTo(HaveOccurred())

	// Update the root Ingress
	_, err = test.Client().Core().Cluster(namespace.ClusterName).NetworkingV1().Ingresses(namespace.Name).
		Apply(test.Ctx(), ingressConfiguration(namespace.Name, name, name+"1", name+"2"), applyOptions)
	test.Expect(err).NotTo(HaveOccurred())

	// Wait until the root Ingress is reconciled with the load balancer Ingresses
	test.Eventually(Ingress(test, namespace, name)).WithTimeout(TestTimeoutMedium).Should(And(
		WithTransform(Annotations, HaveKey(kuadrantcluster.ANNOTATION_HCG_HOST)),
		WithTransform(LoadBalancerIngresses, HaveLen(2)),
	))

	// Retrieve the root Ingress
	ingress = GetIngress(test, namespace, name)

	// Check a DNSRecord for the root Ingress is updated with the expected Spec
	test.Eventually(DNSRecord(test, namespace, name)).Should(PointTo(MatchFields(IgnoreExtras, Fields{
		"Spec": MatchFields(IgnoreExtras,
			Fields{
				"DNSName": Equal(ingress.Annotations[kuadrantcluster.ANNOTATION_HCG_HOST]),
				"Targets": ConsistOf(
					ingress.Status.LoadBalancer.Ingress[0].IP,
					ingress.Status.LoadBalancer.Ingress[1].IP,
				),
				"RecordType": Equal(kuadrantv1.ARecordType),
				"RecordTTL":  Equal(int64(60)),
			}),
	})))
}

func ingressConfiguration(namespace, name string, services ...string) *networkingv1apply.IngressApplyConfiguration {
	var rules []*networkingv1apply.IngressRuleApplyConfiguration
	for _, service := range services {
		rule := networkingv1apply.IngressRule().
			WithHost("test.gblb.com").
			WithHTTP(networkingv1apply.HTTPIngressRuleValue().
				WithPaths(networkingv1apply.HTTPIngressPath().
					WithPath("/").
					WithPathType(networkingv1.PathTypePrefix).
					WithBackend(networkingv1apply.IngressBackend().
						WithService(networkingv1apply.IngressServiceBackend().
							WithName(service).
							WithPort(networkingv1apply.ServiceBackendPort().WithName("http"))))))

		rules = append(rules, rule)
	}

	return networkingv1apply.Ingress(name, namespace).WithSpec(
		networkingv1apply.IngressSpec().WithRules(rules...),
	)
}

func deploymentConfiguration(namespace, name string, labels map[string]string) *appsv1apply.DeploymentApplyConfiguration {
	return appsv1apply.Deployment(name, namespace).
		WithLabels(labels).
		WithSpec(appsv1apply.DeploymentSpec().
			WithSelector(v1apply.LabelSelector().WithMatchLabels(map[string]string{"app": name})).
			WithTemplate(corev1apply.PodTemplateSpec().
				WithLabels(map[string]string{"app": name}).
				WithSpec(corev1apply.PodSpec().
					WithContainers(corev1apply.Container().
						WithName("echo-server").
						WithImage("jmalloc/echo-server").
						WithPorts(corev1apply.ContainerPort().WithName("http").WithContainerPort(8080).WithProtocol(corev1.ProtocolTCP))))))
}

func serviceConfiguration(namespace, name string, labels map[string]string) *corev1apply.ServiceApplyConfiguration {
	return corev1apply.Service(name, namespace).
		WithLabels(labels).
		WithSpec(corev1apply.ServiceSpec().
			WithSelector(map[string]string{"app": name}).
			WithPorts(corev1apply.ServicePort().
				WithName("http").
				WithPort(80).
				WithTargetPort(intstr.FromString("http")).
				WithProtocol(corev1.ProtocolTCP)))
}
