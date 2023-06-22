package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/flyteorg/flyteidl/gen/pb-go/flyteidl/core"
	secretUtils "github.com/flyteorg/flyteplugins/go/tasks/pluginmachinery/utils/secrets"
	flytewebhook "github.com/flyteorg/flytepropeller/pkg/webhook"

	v1 "k8s.io/api/admission/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/caraml-dev/dap-secret-webhook/client"
	"github.com/caraml-dev/dap-secret-webhook/config"
	"github.com/caraml-dev/mlp/api/log"
	"github.com/caraml-dev/mlp/api/pkg/instrumentation/metrics"
)

const RequestsTotal string = "flyte_dsw_webhook_requests_total"

var RequestsTotalMetrics = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: RequestsTotal,
	Help: "Number of request processed by Webhook",
},
	[]string{"project", "status", "operation"},
)

type DAPWebhook struct {
	k8sClientSet kubernetes.Interface
	mlpClient    client.MLPClient
	decoder      runtime.Decoder
}

func NewDAPWebhook(
	k8sClientSet kubernetes.Interface,
	mlpClient client.MLPClient,
	decoder runtime.Decoder,
) DAPWebhook {
	return DAPWebhook{
		k8sClientSet: k8sClientSet,
		mlpClient:    mlpClient,
		decoder:      decoder,
	}
}

/*
Mutate is intended to ingrate Flyte Secret with MLP SecretAPIClient.
On 'Create' Pod invocation, it will create a secret and append env var to the pod.
On 'Delete' Pod invocation, it will delete the secret

The secret name is created with pod name, with secret key as Flyte Secret Key
The secret value is retrieved from MLP with Flyte Secret Key as the key

The env var created follows the same convention Flyte expects - {prefix}-{group}-{key}
however the env var value is tweak to read from the above created secret

Flyte Secret Group is ignored and only key is used
*/
func (pm *DAPWebhook) Mutate(ar v1.AdmissionReview) *v1.AdmissionResponse {

	pod := &corev1.Pod{}
	var admissionResponse *v1.AdmissionResponse
	var err error

	defer func(pod *corev1.Pod, response *v1.AdmissionResponse) {
		status := pod.Namespace != "" && admissionResponse != nil && admissionResponse.Allowed
		RequestsTotalMetrics.WithLabelValues(
			pod.Namespace,
			metrics.GetStatusString(status),
			string(ar.Request.Operation),
		).Inc()
	}(pod, admissionResponse)

	// Pod details are stored in "Object" for Create and "OldObject" for Delete according to
	// https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#request
	if ar.Request.Operation == v1.Create {
		_, _, err := pm.decoder.Decode(ar.Request.Object.Raw, nil, pod)
		if err != nil {
			admissionResponse = toAdmissionResponse(http.StatusBadRequest, err)
		} else {
			log.Infof("received create request for pod: '%v' in namespace: '%v'", pod.Name, pod.Namespace)
			admissionResponse = pm.mutatePodAndCreateSecret(ar, pod)
		}
	} else if ar.Request.Operation == v1.Delete {
		_, _, err = pm.decoder.Decode(ar.Request.OldObject.Raw, nil, pod)
		if err != nil {
			admissionResponse = toAdmissionResponse(http.StatusBadRequest, err)
		} else {
			log.Infof("received delete request for pod: '%v' in namespace: '%v'", pod.Name, pod.Namespace)
			admissionResponse = pm.deleteSecret(ar, pod)
		}
	} else {
		// should never come into this block, by the webhook config's rule
		err = fmt.Errorf("unsupported operation on pod")
		admissionResponse = toAdmissionResponse(http.StatusMethodNotAllowed, err)
	}

	// Log request and response when admission is blocked when there is error
	if !admissionResponse.Allowed {
		jsonData, err := json.Marshal(ar)
		if err != nil {
			admissionResponse = toAdmissionResponse(http.StatusInternalServerError, err)
		}
		log.Errorf("fail to handle request: %v", string(jsonData))
		jsonData, err = json.Marshal(admissionResponse)
		if err != nil {
			admissionResponse = toAdmissionResponse(http.StatusInternalServerError, err)
		}
		log.Errorf("admission err response: %v", string(jsonData))
	}

	return admissionResponse
}

// mutatePodAndCreateSecret inject flyte secrets to the pod as env var, which value are retrieved from mlp client
func (pm *DAPWebhook) mutatePodAndCreateSecret(ar v1.AdmissionReview, pod *corev1.Pod) *v1.AdmissionResponse {
	// get Flyte Secrets from annotation that are injected by Flyte Propeller
	secrets, err := secretUtils.UnmarshalStringMapToSecrets(pod.GetAnnotations())
	if err != nil {
		return toAdmissionResponse(http.StatusInternalServerError, err)
	}

	// k8 secret to be created for the Flyte Task, name of secret will be pod name
	k8secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Data: map[string][]byte{},
		Type: corev1.SecretTypeOpaque,
	}

	// The k8 secret will always be created with a unique id and deleted after
	// Flyte Secret 'Key' is the MLP Secret API "Name"
	for _, secret := range secrets {
		// Inject Flyte secrets as env var to pod, the secretRef is modified here
		pod, err = injectFlyteSecretEnvVar(secret, pod)
		if err != nil {
			return toAdmissionResponse(http.StatusInternalServerError, err)
		}

		secretData, err := pm.mlpClient.GetMLPSecretValue(pod.Namespace, secret.Key)
		if err != nil {
			return toAdmissionResponse(http.StatusInternalServerError, err)
		}
		k8secret.Data[secret.Key] = []byte(secretData)
	}

	log.Infof("injecting %d secrets to pod: '%v' in namespace: '%v'", len(secrets), pod.Name, pod.Namespace)

	err = createK8Secret(pm.k8sClientSet, k8secret)
	if err != nil {
		return toAdmissionResponse(http.StatusInternalServerError, err)
	}

	marshalled, err := json.Marshal(pod)
	if err != nil {
		return toAdmissionResponse(http.StatusInternalServerError, err)
	}

	response := admission.PatchResponseFromRaw(ar.Request.Object.Raw, marshalled)
	adminResponse := &response.AdmissionResponse
	adminResponse.Patch, err = json.Marshal(response.Patches)
	if err != nil {
		return toAdmissionResponse(http.StatusInternalServerError, err)
	}
	return adminResponse
}

// deleteSecret deletes the secret that was created along with the pod. No modification to pod is required
func (pm *DAPWebhook) deleteSecret(_ v1.AdmissionReview, pod *corev1.Pod) *v1.AdmissionResponse {
	// the secrets are created with podName in the same namespace
	if err := deleteK8Secret(pm.k8sClientSet, pod.Namespace, pod.Name); err != nil {
		return toAdmissionResponse(http.StatusInternalServerError, err)
	}
	return &v1.AdmissionResponse{Allowed: true}
}

// toAdmissionResponse return an AdmissionResponse with the error.
func toAdmissionResponse(code int32, err error) *v1.AdmissionResponse {
	ar := admission.Errored(code, err).AdmissionResponse
	return &ar
}

// injectFlyteSecretEnvVar inject secret as env var onto pod using flyte library which holds the convention
// of env var for the secrets to be loaded into FlyteContext. Modification is done only to the "ValueFrom" of the
// env var, so that it reads
func injectFlyteSecretEnvVar(secret *core.Secret, p *corev1.Pod) (newP *corev1.Pod, err error) {
	// secret group is expected to be empty
	if len(secret.Key) == 0 {
		return nil, fmt.Errorf("webhook require secretkey to be set. "+
			"Secret: [%v]", secret)
	}

	switch secret.MountRequirement {
	case core.Secret_ANY:
		fallthrough
	case core.Secret_ENV_VAR:
		envVar := flytewebhook.CreateEnvVarForSecret(secret)
		// This is where the envVar is tweak to use pod name as name of the secret
		envVar.ValueFrom.SecretKeyRef.LocalObjectReference = corev1.LocalObjectReference{
			Name: p.Name,
		}
		p.Spec.InitContainers = flytewebhook.AppendEnvVars(p.Spec.InitContainers, envVar)
		p.Spec.Containers = flytewebhook.AppendEnvVars(p.Spec.Containers, envVar)

		prefixEnvVar := corev1.EnvVar{
			Name:  flytewebhook.SecretEnvVarPrefix,
			Value: flytewebhook.K8sDefaultEnvVarPrefix,
		}

		p.Spec.InitContainers = flytewebhook.AppendEnvVars(p.Spec.InitContainers, prefixEnvVar)
		p.Spec.Containers = flytewebhook.AppendEnvVars(p.Spec.Containers, prefixEnvVar)
	default:
		err := fmt.Errorf("unrecognized mount requirement [%v] for secret [%v]", secret.MountRequirement.String(), secret.Key)
		return p, err
	}
	return p, nil
}

// createK8Secret create the secret if it doesn't exist, else it does nothing
func createK8Secret(clientSet kubernetes.Interface, k8secret *corev1.Secret) error {
	_, err := clientSet.CoreV1().Secrets(k8secret.Namespace).Get(context.Background(), k8secret.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			_, err := clientSet.CoreV1().Secrets(k8secret.Namespace).Create(context.Background(), k8secret, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create mlpSecret: %v", err)
			}
		} else {
			return err
		}
	}
	log.Infof("created k8 secret: '%v' in namespace: '%v'", k8secret.Name, k8secret.Namespace)
	return nil
}

// deleteK8Secret deletes the secret if it exists, else it does nothing
func deleteK8Secret(clientSet kubernetes.Interface, namespace string, secretName string) error {
	_, err := clientSet.CoreV1().Secrets(namespace).Get(context.Background(), secretName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		} else {
			return err
		}
	}
	err = clientSet.CoreV1().Secrets(namespace).Delete(context.Background(), secretName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete mlpSecret: %v", err)
	}
	log.Infof("deleted k8 secret: '%v' in namespace: '%v'", secretName, namespace)
	return nil
}

func generateMutatingWebhookConfig(webhookConfig config.WebhookConfig, caCertFilePath string) (*admissionregistrationv1.MutatingWebhookConfiguration, error) {
	caBytes, err := os.ReadFile(caCertFilePath)
	if err != nil {
		return nil, err
	}
	fail := admissionregistrationv1.Fail
	sideEffects := admissionregistrationv1.SideEffectClassNoneOnDryRun

	mutateConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookConfig.Name,
			Namespace: webhookConfig.Namespace,
			Labels: map[string]string{
				"app": webhookConfig.Name,
			},
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				// needs to be a valid dns
				Name: webhookConfig.WebhookName,
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: caBytes, // CA bundle created earlier
					Service: &admissionregistrationv1.ServiceReference{
						Name:      webhookConfig.ServiceName,
						Namespace: webhookConfig.ServiceNamespace,
						Path:      &webhookConfig.MutatePath,
						Port:      &webhookConfig.ServicePort,
					},
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
							admissionregistrationv1.Delete,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"pods"},
						},
					},
				},
				FailurePolicy: &fail,
				SideEffects:   &sideEffects,
				AdmissionReviewVersions: []string{
					"v1",
				},
				ObjectSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						secretUtils.PodLabel: secretUtils.PodLabelValue,
					},
				},
			}},
	}

	return mutateConfig, nil
}

// CreateOrUpdateMutatingWebhookConfig will create/update the MutatingWebhookConfiguration.
// It will read the CA file, so if there are any update to the bundle, the CA will be updated
func CreateOrUpdateMutatingWebhookConfig(k8sClient kubernetes.Interface, webhookConfig config.WebhookConfig, caCertFilePath string) error {

	mutateConfig, err := generateMutatingWebhookConfig(webhookConfig, caCertFilePath)
	if err != nil {
		return err
	}

	webhookClient := k8sClient.AdmissionregistrationV1().MutatingWebhookConfigurations()
	ctx := context.Background()

	log.Infof("Creating MutatingWebhookConfiguration")
	_, err = webhookClient.Create(ctx, mutateConfig, metav1.CreateOptions{})

	if err != nil && k8errors.IsAlreadyExists(err) {
		log.Infof("Failed to create MutatingWebhookConfiguration. Will attempt to update. Error: %v", err)
		obj, getErr := webhookClient.Get(ctx, mutateConfig.Name, metav1.GetOptions{})
		if getErr != nil {
			log.Infof("Failed to get MutatingWebhookConfiguration. Error: %v", getErr)
			return err
		}

		obj.Webhooks = mutateConfig.Webhooks
		_, err = webhookClient.Update(ctx, obj, metav1.UpdateOptions{})
		if err != nil {
			log.Infof("Failed to update existing mutating webhook config. Error: %v", err)
			return err
		}
	} else if err != nil {
		log.Infof("Failed to create MutatingWebhookConfiguration. Error: %v", err)
		return err
	}

	log.Infof("MutatingWebhookConfiguration configured")
	return nil
}
