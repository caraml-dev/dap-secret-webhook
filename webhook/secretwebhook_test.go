package webhook

import (
	"github.com/caraml-dev/dap-secret-webhook/config"
	"github.com/flyteorg/flyteidl/gen/pb-go/flyteidl/core"
	"github.com/flyteorg/flyteplugins/go/tasks/pluginmachinery/utils/secrets"
	"github.com/stretchr/testify/assert"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"os"
	"testing"

	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/yaml"

	"github.com/caraml-dev/dap-secret-webhook/test/mocks"
)

var admissionScheme = runtime.NewScheme()
var codecs = serializer.NewCodecFactory(admissionScheme)

func init() {
	utilruntime.Must(v1.AddToScheme(admissionScheme))
}

const (
	secretGroup = "testgroup"
	secretKey   = "testsecretkey"
)

func TestMutate(t *testing.T) {
	mlpClient := &mocks.MLPClient{}
	mlpClient.On("GetMLPSecretValue", secretGroup, secretKey).Return("secret_data", nil)
	dapWebhook := NewDAPWebhook(fake.NewSimpleClientset(), mlpClient, codecs.UniversalDeserializer())
	jsonPatchType := v1.PatchTypeJSONPatch

	yamlData, err := os.ReadFile("../test/mutate/pod_with_secret.yaml")
	assert.NoError(t, err)
	podWithSecret, err := yaml.YAMLToJSON(yamlData)
	assert.NoError(t, err)

	type args struct {
		req            *v1.AdmissionReview
		additionalFunc func()
	}
	var tests = []struct {
		name string
		args args
		resp *v1.AdmissionResponse
	}{
		{
			name: "ok create",
			args: args{
				req: &v1.AdmissionReview{
					Request: &v1.AdmissionRequest{
						Operation: "CREATE",
						// flyte.secrets/s0 is encoded using Group: 'TestGroup', Key: 'TestSecretKey'
						Object: runtime.RawExtension{
							Raw: podWithSecret,
						},
					},
				},
				additionalFunc: func() {
					mlpClient.AssertCalled(t, "GetMLPSecretValue", secretGroup, secretKey)
				},
			},
			// Expect an 'add' patch with env var _FSEC_{Group}_{Key} with value from secret named {pod_name}, with key {Key}
			// and another prefix env by flyte '_FSEC_'
			resp: &v1.AdmissionResponse{
				Allowed: true,
				Patch: []byte(`[{"op":"add","path":"/spec/containers/0/env",` +
					`"value":[{"name":"_FSEC_TESTGROUP_TESTSECRETKEY","valueFrom":{"secretKeyRef":{"key":"testsecretkey",` +
					`"name":"pod-with-secret","optional":true}}},{"name":"FLYTE_SECRETS_ENV_PREFIX","value":"_FSEC_"}]}]`),
				PatchType: &jsonPatchType,
			},
		},
		{
			name: "ok delete",
			args: args{
				req: &v1.AdmissionReview{
					Request: &v1.AdmissionRequest{
						Operation: "DELETE",
						OldObject: runtime.RawExtension{
							Raw: podWithSecret,
						},
					},
				},
			},
			resp: &v1.AdmissionResponse{
				Allowed: true,
			},
		},
		{
			name: "invalid operation",
			args: args{
				req: &v1.AdmissionReview{
					Request: &v1.AdmissionRequest{
						Operation: "PATCH",
					},
				},
			},
			resp: &v1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Message: "unsupported operation on pod",
				},
			},
		},
		{
			name: "invalid secret request",
			args: args{
				req: &v1.AdmissionReview{
					Request: &v1.AdmissionRequest{
						Operation: "CREATE",
						Object: runtime.RawExtension{
							//annotation created with empty key
							Raw: []byte(`{"metadata":{"annotations":{"flyte.secrets/s0":"m4zg54lqhiqcevdfon1eo3tpovycectnn41w34c6ojsxc4ljojsw1zlooq4carkokzpvmqksbi"}}}`),
						},
					},
				},
			},
			resp: &v1.AdmissionResponse{
				Allowed: false,
				Result: &metav1.Status{
					Message: `webhook require secretkey to be set. Secret: [group:"TestGroup" mount_requirement:ENV_VAR ]`,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			admissionResponse := dapWebhook.Mutate(*tt.args.req)
			if tt.args.additionalFunc != nil {
				tt.args.additionalFunc()
			}
			//fmt.Println(string(admissionResponse.Patch))
			assert.Equal(t, tt.resp, admissionResponse)
		})
	}
}

func TestMutatingWebhookConfig(t *testing.T) {

	// namespace is skipped due to limitation in fake.NewSimpleClientset
	config := config.WebhookConfig{
		Name:        "wh_name",
		ServiceName: "service-name",
		WebhookName: "local.cluster.svc",
		ServicePort: 8080,
		MutatePath:  "/test",
	}
	certPath := "../test/mutate/dummy.txt"
	// any file
	output, err := generateMutatingWebhookConfig(config, certPath)
	assert.NoError(t, err)

	yamlData, err := os.ReadFile("../test/mutate/webhook.yaml")
	assert.NoError(t, err)
	obj, _, err := codecs.UniversalDeserializer().Decode(yamlData, nil, &admissionregistrationv1.MutatingWebhookConfiguration{})
	assert.NoError(t, err)
	assert.Equal(t, obj, output)

	k8Client := fake.NewSimpleClientset(&admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: config.Namespace,
		}})

	err = CreateOrUpdateMutatingWebhookConfig(k8Client, config, certPath)
	assert.NoError(t, err)
}

// this is used to check the secret used in /test/mutate/pod_with_secret.yaml is using an encrypted secret expected by other test
func TestAnnotation(t *testing.T) {
	got, err := secrets.UnmarshalStringMapToSecrets(map[string]string{
		"flyte.secrets/s0": "m4zg54lqhiqce4dfon1go3tpovycectlmv3tuibcorsxg4dtmvrxezlunnsxsiqknvxxk2tul4zgk3lvnfzgk2lfnz1duicfjzlf5vsbkifa",
	})
	expected := &core.Secret{
		Group:            secretGroup,
		Key:              secretKey,
		MountRequirement: 1,
	}
	assert.NoError(t, err)
	assert.Equal(t, 1, len(got))
	assert.Equal(t, expected, got[0])
}
