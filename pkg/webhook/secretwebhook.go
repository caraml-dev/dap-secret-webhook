package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/antihax/optional"
	"github.com/flyteorg/flyteidl/gen/pb-go/flyteidl/core"
	secretUtils "github.com/flyteorg/flyteplugins/go/tasks/pluginmachinery/utils/secrets"
	flytewebhook "github.com/flyteorg/flytepropeller/pkg/webhook"

	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	mlp "github.com/caraml-dev/mlp/api/client"
	"github.com/caraml-dev/mlp/api/log"
)

const (
	mlpQueryTimeoutSeconds = 30
)

type secretInjector struct {
	k8sClientSet *kubernetes.Clientset
	mlpClient    *mlp.APIClient
	decoder      runtime.Decoder
}

func NewSecretInjector(
	k8sClientSet *kubernetes.Clientset,
	mlpClient *mlp.APIClient,
	decoder runtime.Decoder,
) secretInjector {
	return secretInjector{
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
func (pm *secretInjector) Mutate(ar v1.AdmissionReview) *v1.AdmissionResponse {

	pod := &corev1.Pod{}
	var admissionResponse *v1.AdmissionResponse
	var err error

	// Pod details are stored in "Object" for Create and "OldObject" for Delete according to
	// https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#request
	if ar.Request.Operation == v1.Create {
		_, _, err := pm.decoder.Decode(ar.Request.Object.Raw, nil, pod)
		if err != nil {
			admissionResponse = toV1AdmissionResponse(err)
		} else {
			admissionResponse = pm.mutatePodAndCreateSecret(ar, pod)
		}
	} else if ar.Request.Operation == v1.Delete {
		_, _, err = pm.decoder.Decode(ar.Request.OldObject.Raw, nil, pod)
		if err != nil {
			admissionResponse = toV1AdmissionResponse(err)
		} else {
			admissionResponse = pm.deleteSecret(ar, pod)
		}
	} else {
		// should never come into this block, by the webhook config's rule
		err = fmt.Errorf("unsupported operation on pod")
		admissionResponse = toV1AdmissionResponse(err)
	}

	// Log request and response when admission is blocked when there is error
	if !admissionResponse.Allowed {
		jsonData, err := json.Marshal(ar)
		if err != nil {
			return toV1AdmissionResponse(err)
		}
		log.Errorf("fail to handle request: %v", string(jsonData))
		jsonData, err = json.Marshal(admissionResponse)
		if err != nil {
			return toV1AdmissionResponse(err)
		}
		log.Errorf("fail to handle request, error: %v", string(jsonData))
	}

	return admissionResponse
}

// mutatePodAndCreateSecret inject flyte secrets to the pod as env var, which value are retrieved from mlp client
func (pm *secretInjector) mutatePodAndCreateSecret(ar v1.AdmissionReview, pod *corev1.Pod) *v1.AdmissionResponse {
	// get Flyte Secrets from annotation that are injected by Flyte Propeller
	secrets, err := secretUtils.UnmarshalStringMapToSecrets(pod.GetAnnotations())
	if err != nil {
		return toV1AdmissionResponse(err)
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

	mlpProject, err := getMLPProject(pm.mlpClient, pod.Namespace)
	if err != nil {
		return toV1AdmissionResponse(err)
	}

	mlpSecrets, err := getMLPSecrets(pm.mlpClient, mlpProject.ID)
	if err != nil {
		return toV1AdmissionResponse(err)
	}

	// The k8 secret will always be created with a unique id and deleted after
	// Flyte Secret 'Key' is the MLP Secret API "Name"
	for _, secret := range secrets {
		// Inject Flyte secrets as env var to pod, the secretRef is modified here
		pod, err = injectFlyteSecretEnvVar(secret, pod)
		if err != nil {
			return toV1AdmissionResponse(err)
		}

		secretData, err := getMLPSecretValue(mlpSecrets, secret.Key)
		if err != nil {
			return toV1AdmissionResponse(err)
		}
		k8secret.Data[secret.Key] = []byte(secretData)

	}

	err = createK8Secret(pm.k8sClientSet, k8secret)
	if err != nil {
		return toV1AdmissionResponse(err)
	}

	marshalled, err := json.Marshal(pod)
	if err != nil {
		return toV1AdmissionResponse(err)
	}

	response := admission.PatchResponseFromRaw(ar.Request.Object.Raw, marshalled)
	adminResponse := &response.AdmissionResponse
	adminResponse.Patch, err = json.Marshal(response.Patches)
	if err != nil {
		return toV1AdmissionResponse(err)
	}
	return adminResponse
}

// deleteSecret deletes the secret that was created along with the pod. No modification to pod is required
func (pm *secretInjector) deleteSecret(_ v1.AdmissionReview, pod *corev1.Pod) *v1.AdmissionResponse {
	// the secrets are created with podName in the same namespace
	if err := deleteK8Secret(pm.k8sClientSet, pod.Namespace, pod.Name); err != nil {
		return toV1AdmissionResponse(err)
	}
	return &v1.AdmissionResponse{Allowed: true}
}

// toV1AdmissionResponse return an AdmissionResponse with the error. "allowed" is set to false by default
func toV1AdmissionResponse(err error) *v1.AdmissionResponse {
	return &v1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

// injectFlyteSecretEnvVar inject secret as env var onto pod using flyte library which holds the convention
// of env var for the secrets to be loaded into FlyteContext. Modification is done only to the "ValueFrom" of the
// env var, so that it reads
func injectFlyteSecretEnvVar(secret *core.Secret, p *corev1.Pod) (newP *corev1.Pod, err error) {
	// secret is expected to be empty
	if len(secret.Key) == 0 {
		return nil, fmt.Errorf("k8s Secrets Webhook require key to be set. "+
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

func getMLPProject(client *mlp.APIClient, namespace string) (*mlp.Project, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mlpQueryTimeoutSeconds*time.Second)
	defer cancel()

	var options *mlp.ProjectApiV1ProjectsGetOpts
	if len(namespace) > 0 {
		options = &mlp.ProjectApiV1ProjectsGetOpts{
			Name: optional.NewString(namespace),
		}
	}
	projects, resp, err := client.ProjectApi.V1ProjectsGet(ctx, options)
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	for _, project := range projects {
		if project.Name == namespace {
			return &project, nil
		}
	}
	return nil, fmt.Errorf("cannot find project from mlp client")

}

func getMLPSecrets(client *mlp.APIClient, projectId int32) ([]mlp.Secret, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mlpQueryTimeoutSeconds*time.Second)
	defer cancel()

	secrets, resp, err := client.SecretApi.V1ProjectsProjectIdSecretsGet(ctx, projectId)
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	return secrets, nil
}

func getMLPSecretValue(secrets []mlp.Secret, name string) (string, error) {
	for _, mlpSecret := range secrets {
		if mlpSecret.Name == name {
			return mlpSecret.Data, nil
		}
	}
	return "", fmt.Errorf("cannot find secret value from mlp")
}

// createK8Secret create the secret if it doesn't exist, else it does nothing
func createK8Secret(clientSet *kubernetes.Clientset, k8secret *corev1.Secret) error {
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
	return nil
}

// deleteK8Secret deletes the secret if it exists, else it does nothing
func deleteK8Secret(clientSet *kubernetes.Clientset, namespace string, secretName string) error {
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
	return nil
}
