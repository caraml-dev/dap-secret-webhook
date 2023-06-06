package webhook

// The implementation are mostly taken from
// https://github.com/kubernetes/kubernetes/blob/release-1.21/test/images/agnhost/webhook/main.go
// https://github.com/flyteorg/flytepropeller/tree/master/pkg/webhook

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/caraml-dev/dap-secret-webhook/client"
	"github.com/caraml-dev/dap-secret-webhook/webhook"

	"github.com/spf13/cobra"

	"github.com/caraml-dev/dap-secret-webhook/config"
	mlp "github.com/caraml-dev/mlp/api/client"
	"github.com/caraml-dev/mlp/api/log"
	"github.com/caraml-dev/mlp/api/pkg/auth"

	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var CmdWebhook = &cobra.Command{
	Use:   "webhook",
	Short: "Starts a HTTP server, which run DAP Secret Webhook",
	Long:  `Starts a HTTP server, which run DAP Secret Webhook. This will attach secret to Flyte Pod from MLP API`,
	Run:   run,
}

var admissionScheme = runtime.NewScheme()
var codecs = serializer.NewCodecFactory(admissionScheme)

func init() {
	utilruntime.Must(v1.AddToScheme(admissionScheme))
}

func initK8Client(incluster bool) (*kubernetes.Clientset, error) {

	var config *rest.Config
	var err error

	if incluster {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create client: %v", err)
		}
	} else {
		kubeconfigPath := os.Getenv("KUBECONFIG")
		if kubeconfigPath == "" {
			// If KUBECONFIG is not set, use the default kubeconfig path
			kubeconfigPath = clientcmd.RecommendedHomeFile
		}
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create client: %v", err)
		}
	}

	// Create a new Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %v", err)
	}
	return clientset, nil
}

func initMLPClient(mlpApiHost string) client.MLPClient {
	httpClient := http.DefaultClient

	googleClient, err := auth.InitGoogleClient(context.Background())
	if err == nil {
		httpClient = googleClient
	} else {
		log.Infof("Google default credential not found. Fallback to HTTP default client")
	}
	cfg := mlp.NewConfiguration()
	cfg.BasePath = mlpApiHost
	cfg.HTTPClient = httpClient

	return &client.APIClient{
		APIClient: *mlp.NewAPIClient(cfg),
	}
}

// admitV1Func handles a v1 admission
type admitV1Func func(v1.AdmissionReview) *v1.AdmissionResponse

// serve handles the http portion of a request prior to handing to an admit
// function
func serve(w http.ResponseWriter, r *http.Request, admit admitV1Func) {
	var body []byte
	if r.Body != nil {
		if data, err := io.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		log.Errorf("contentType=%s, expect application/json", contentType)
		return
	}

	deserializer := codecs.UniversalDeserializer()
	obj, gvk, err := deserializer.Decode(body, nil, nil)
	if err != nil {
		msg := fmt.Sprintf("Request could not be decoded: %v", err)
		log.Errorf(msg)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	var responseObj runtime.Object
	switch *gvk {
	case v1.SchemeGroupVersion.WithKind("AdmissionReview"):
		requestedAdmissionReview, ok := obj.(*v1.AdmissionReview)
		if !ok {
			log.Errorf("Expected v1.AdmissionReview but got: %T", obj)
			return
		}
		responseAdmissionReview := &v1.AdmissionReview{}
		responseAdmissionReview.SetGroupVersionKind(*gvk)
		responseAdmissionReview.Response = admit(*requestedAdmissionReview)
		responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID
		responseObj = responseAdmissionReview
	default:
		msg := fmt.Sprintf("Unsupported group version kind: %v", gvk)
		log.Errorf(msg)
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	respBytes, err := json.Marshal(responseObj)
	if err != nil {
		log.Errorf("unable to marshal response: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(respBytes); err != nil {
		log.Errorf("unable to write response: %v", err)
	}
}

func serveMutate(k8sClient *kubernetes.Clientset,
	mlpClient client.MLPClient) func(w http.ResponseWriter, r *http.Request) {

	dapWebhook := webhook.NewDAPWebhook(k8sClient, mlpClient, codecs.UniversalDeserializer())

	return func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, dapWebhook.Mutate)
	}
}

func configTLS(certFile string, keyFile string) *tls.Config {
	sCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		panic(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{sCert},
	}
}

func run(cmd *cobra.Command, args []string) {

	cfg, err := config.InitConfigEnv()
	if err != nil {
		panic(err)
	}

	k8sClient, err := initK8Client(cfg.InCluster)
	if err != nil {
		panic(err)
	}
	mlpClient := initMLPClient(cfg.MLPConfig.APIHost)

	err = webhook.CreateOrUpdateMutatingWebhookConfig(k8sClient, cfg.WebhookConfig, cfg.TLSConfig.CaCertFile)
	if err != nil {
		panic(err)
	}

	http.HandleFunc(cfg.WebhookConfig.MutatePath, serveMutate(k8sClient, mlpClient))
	server := &http.Server{
		Addr:      fmt.Sprintf(":%d", cfg.WebhookConfig.ServicePort),
		TLSConfig: configTLS(cfg.TLSConfig.ServerCertFile, cfg.TLSConfig.ServerKeyFile),
	}
	log.Infof("listening")
	err = server.ListenAndServeTLS("", "")
	if err != nil {
		panic(err)
	}
}
