# dap-secret-webhook

## Overview
dap-secret-webhook is a Kubernetes pod mutating webhook for using CaraML Secrets in Flyte.

When Flyte Secret is used in a Flyte workflow, the created pod that runs the task will be injected with predefined Flyte labels, with the Secret metadata in pod annotations.

DAP Secret Webhook Server will read the Flyte Secret metadata from the annotations and f
- On startup, create a `MutatingWebhookConfiguration` that calls the webhook server for pod create/delete with the predefined Flyte labels
- Read the Flyte Secret Metadata and fetch the Secret Data from MLP
- Create a k8 Secret resource and mount it as env var to the pod, in an expected format by Flyte Secret Manager

Reference  
- [Kubernetes Webhook](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
- [Kubernetes Webhook Test](https://github.com/kubernetes/kubernetes/tree/release-1.24/test/images/agnhost/webhook)
- [Flyte Webhook Implement](https://github.com/flyteorg/flytepropeller/tree/master/pkg/webhook)

### Prerequisite 
- Flyte Native Webhook to be disabled
- [MLP](https://github.com/caraml-dev/mlp/tree/main)
- TLS Server Key/Cert and CA certs generated
- Environment variables configured

### Environment Variable
| Name                      | Default                                    | Description                                                                  |
|---------------------------|--------------------------------------------|------------------------------------------------------------------------------|
| TLS_SERVER_CERT_FILE      | -                                          | Server Cert                                                                  |
| TLS_SERVER_KEY_FILE       | -                                          | Server Key                                                                   |
| TLS_CA_CERT_FILE          | -                                          | CA Public Cert                                                               |
| MLP_API_HOST              | -                                          | MLP API Host                                                                 |
| WEBHOOK_NAME              | dap-secret-webhook                         | Name of the MutatingWebhookConfiguration resource                            |
| WEBHOOK_NAMESPACE         | flyte                                      | Namespace of the MutatingWebhookConfiguration                                |
| WEBHOOK_WEBHOOK_NAME      | dap-secret-webhook.flyte.svc.cluster.local | Name of the webhook to call. Needs to be qualified name                      |
| WEBHOOK_SERVICE_NAME      | dap-secret-webhook                         | Name of the service for the webhook to call when a request fulfill the rules |
| WEBHOOK_SERVICE_NAMESPACE | flyte                                      | Namespace of the service deployed in cluster                                 |
| WEBHOOK_SERVICE_PORT      | 443                                        | Port of the service                                                          |
| WEBHOOK_MUTATE_PATH       | /mutate                                    | Endpoint of the service to call for mutate function                          |

### Folder Structure
    .        
    ├── client                  # MLP CLient
    ├── cmd                     # Entrypoint
    ├── config                  # Configuration
    ├── test                    # Test data and mocks
    ├── webhook                 # Webhook Server
    └── README.md