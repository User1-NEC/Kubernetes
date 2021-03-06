/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apiextensions-apiserver/test/integration/fixtures"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/klog/v2"
	kubeapiservertesting "k8s.io/kubernetes/cmd/kube-apiserver/app/testing"

	"k8s.io/kubernetes/test/integration/framework"
)

var (
	invalidBodyJSON = `
	{
		"apiVersion": "apps/v1",
		"kind": "Deployment",
		"metadata": {
			"name": "%s",
			"labels": {"app": "nginx"}
		},
		"spec": {
			"unknown1": "val1",
			"unknownDupe": "valDupe",
			"unknownDupe": "valDupe2",
			"paused": true,
			"paused": false,
			"selector": {
				"matchLabels": {
					"app": "nginx"
				}
			},
			"template": {
				"metadata": {
					"labels": {
						"app": "nginx"
					}
				},
				"spec": {
					"containers": [{
						"name":  "nginx",
						"image": "nginx:latest",
						"unknownNested": "val1",
						"imagePullPolicy": "Always",
						"imagePullPolicy": "Never"
					}]
				}
			}
		}
	}
		`

	invalidBodyYAML = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  labels:
    app: nginx
spec:
  unknown1: val1
  unknownDupe: valDupe
  unknownDupe: valDupe2
  paused: true
  paused: false
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
      - name: nginx
        image: nginx:latest
        unknownNested: val1
        imagePullPolicy: Always
        imagePullPolicy: Never`

	validBodyJSON = `
{
	"apiVersion": "apps/v1",
	"kind": "Deployment",
	"metadata": {
		"name": "%s",
		"labels": {"app": "nginx"}
	},
	"spec": {
		"selector": {
			"matchLabels": {
				"app": "nginx"
			}
		},
		"template": {
			"metadata": {
				"labels": {
					"app": "nginx"
				}
			},
			"spec": {
				"containers": [{
					"name":  "nginx",
					"image": "nginx:latest",
					"imagePullPolicy": "Always"
				}]
			}
		}
	}
}`
	applyInvalidBody = `{
		"apiVersion": "apps/v1",
		"kind": "Deployment",
		"metadata": {
			"name": "%s",
			"labels": {"app": "nginx"}
		},
		"spec": {
			"paused": false,
			"paused": true,
			"selector": {
				"matchLabels": {
					"app": "nginx"
				}
			},
			"template": {
				"metadata": {
					"labels": {
						"app": "nginx"
					}
				},
				"spec": {
					"containers": [{
						"name":  "nginx",
						"image": "nginx:latest",
						"imagePullPolicy": "Never",
						"imagePullPolicy": "Always"
					}]
				}
			}
		}
	}`
	crdInvalidBody = `
{
	"apiVersion": "%s",
	"kind": "%s",
	"metadata": {
		"name": "%s",
		"resourceVersion": "%s"
	},
	"spec": {
		"unknown1": "val1",
		"unknownDupe": "valDupe",
		"unknownDupe": "valDupe2",
		"knownField1": "val1",
		"knownField1": "val2",
			"ports": [{
				"name": "portName",
				"containerPort": 8080,
				"protocol": "TCP",
				"hostPort": 8081,
				"hostPort": 8082,
				"unknownNested": "val"
			}]
	}
}`

	crdValidBody = `
{
	"apiVersion": "%s",
	"kind": "%s",
	"metadata": {
		"name": "%s"
	},
	"spec": {
		"knownField1": "val1",
			"ports": [{
				"name": "portName",
				"containerPort": 8080,
				"protocol": "TCP",
				"hostPort": 8081
			}]
	}
}
	`

	crdInvalidBodyYAML = `
apiVersion: "%s"
kind: "%s"
metadata:
  name: "%s"
  resourceVersion: "%s"
spec:
  unknown1: val1
  unknownDupe: valDupe
  unknownDupe: valDupe2
  knownField1: val1
  knownField1: val2
  ports:
  - name: portName
    containerPort: 8080
    protocol: TCP
    hostPort: 8081
    hostPort: 8082
    unknownNested: val`

	crdApplyInvalidBody = `
{
	"apiVersion": "%s",
	"kind": "%s",
	"metadata": {
		"name": "%s"
	},
	"spec": {
		"knownField1": "val1",
		"knownField1": "val2",
		"ports": [{
			"name": "portName",
			"containerPort": 8080,
			"protocol": "TCP",
			"hostPort": 8081,
			"hostPort": 8082
		}]
	}
}`

	crdApplyValidBody = `
{
	"apiVersion": "%s",
	"kind": "%s",
	"metadata": {
		"name": "%s"
	},
	"spec": {
		"knownField1": "val1",
		"ports": [{
			"name": "portName",
			"containerPort": 8080,
			"protocol": "TCP",
			"hostPort": 8082
		}]
	}
}`

	patchYAMLBody = `
apiVersion: %s
kind: %s
metadata:
  name: %s
  finalizers:
  - test-finalizer
spec:
  cronSpec: "* * * * */5"
  ports:
  - name: x
    containerPort: 80
    protocol: TCP
`

	crdSchemaBase = `
{
		"openAPIV3Schema": {
			"type": "object",
			"properties": {
				"spec": {
					"type": "object",
					%s
					"properties": {
						"cronSpec": {
							"type": "string",
							"pattern": "^(\\d+|\\*)(/\\d+)?(\\s+(\\d+|\\*)(/\\d+)?){4}$"
						},
						"knownField1": {
							"type": "string"
						},
						"ports": {
							"type": "array",
							"x-kubernetes-list-map-keys": [
								"containerPort",
								"protocol"
							],
							"x-kubernetes-list-type": "map",
							"items": {
								"properties": {
									"containerPort": {
										"format": "int32",
										"type": "integer"
									},
									"hostIP": {
										"type": "string"
									},
									"hostPort": {
										"format": "int32",
										"type": "integer"
									},
									"name": {
										"type": "string"
									},
									"protocol": {
										"type": "string"
									}
								},
								"required": [
									"containerPort",
									"protocol"
								],
								"type": "object"
							}
						}
					}
				}
			}
		}
	}
	`
)

// TestFieldValidationPost tests POST requests containing unknown fields with
// strict and non-strict field validation.
func TestFieldValidationPost(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()

	_, client, closeFn := setup(t)
	defer closeFn()

	var testcases = []struct {
		name                   string
		bodyBase               string
		opts                   metav1.CreateOptions
		contentType            string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "post-strict-validation",
			opts: metav1.CreateOptions{
				FieldValidation: "Strict",
			},
			bodyBase: invalidBodyJSON,
			strictDecodingErrors: []string{
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
				`duplicate field "paused"`,
				// note: fields that are both unknown
				// and duplicated will only be detected
				// as unknown for typed resources.
				`unknown field "unknownNested"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "post-warn-validation",
			opts: metav1.CreateOptions{
				FieldValidation: "Warn",
			},
			bodyBase: invalidBodyJSON,
			strictDecodingWarnings: []string{
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
				`duplicate field "paused"`,
				// note: fields that are both unknown
				// and duplicated will only be detected
				// as unknown for typed resources.
				`unknown field "unknownNested"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "post-ignore-validation",
			opts: metav1.CreateOptions{
				FieldValidation: "Ignore",
			},
			bodyBase: invalidBodyJSON,
		},
		{
			name:     "post-default-ignore-validation",
			bodyBase: invalidBodyJSON,
			strictDecodingWarnings: []string{
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
				`duplicate field "paused"`,
				// note: fields that are both unknown
				// and duplicated will only be detected
				// as unknown for typed resources.
				`unknown field "unknownNested"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "post-strict-validation-yaml",
			opts: metav1.CreateOptions{
				FieldValidation: "Strict",
			},
			bodyBase:    invalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingErrors: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "paused" already set in map`,
				`line 26: key "imagePullPolicy" already set in map`,
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
				`unknown field "unknownNested"`,
			},
		},
		{
			name: "post-warn-validation-yaml",
			opts: metav1.CreateOptions{
				FieldValidation: "Warn",
			},
			bodyBase:    invalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "paused" already set in map`,
				`line 26: key "imagePullPolicy" already set in map`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
			},
		},
		{
			name: "post-ignore-validation-yaml",
			opts: metav1.CreateOptions{
				FieldValidation: "Ignore",
			},
			bodyBase:    invalidBodyYAML,
			contentType: "application/yaml",
		},
		{
			name:        "post-no-validation-yaml",
			bodyBase:    invalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "paused" already set in map`,
				`line 26: key "imagePullPolicy" already set in map`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(tc.bodyBase, fmt.Sprintf("test-deployment-%s", tc.name)))
			req := client.CoreV1().RESTClient().Post().
				AbsPath("/apis/apps/v1").
				Namespace("default").
				Resource("deployments").
				SetHeader("Content-Type", tc.contentType).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body(body).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected request err: %v", result.Error())
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %v", strictErr, result.Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}
			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationPut tests PUT requests
// that update existing objects with unknown fields
// for both strict and non-strict field validation.
func TestFieldValidationPut(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()

	_, client, closeFn := setup(t)
	defer closeFn()

	deployName := "test-deployment"
	postBody := []byte(fmt.Sprintf(string(validBodyJSON), deployName))

	if _, err := client.CoreV1().RESTClient().Post().
		AbsPath("/apis/apps/v1").
		Namespace("default").
		Resource("deployments").
		Body(postBody).
		DoRaw(context.TODO()); err != nil {
		t.Fatalf("failed to create initial deployment: %v", err)
	}

	var testcases = []struct {
		name                   string
		opts                   metav1.UpdateOptions
		putBodyBase            string
		contentType            string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "put-strict-validation",
			opts: metav1.UpdateOptions{
				FieldValidation: "Strict",
			},
			putBodyBase: invalidBodyJSON,
			strictDecodingErrors: []string{
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
				`duplicate field "paused"`,
				// note: fields that are both unknown
				// and duplicated will only be detected
				// as unknown for typed resources.
				`unknown field "unknownNested"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "put-warn-validation",
			opts: metav1.UpdateOptions{
				FieldValidation: "Warn",
			},
			putBodyBase: invalidBodyJSON,
			strictDecodingWarnings: []string{
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
				`duplicate field "paused"`,
				// note: fields that are both unknown
				// and duplicated will only be detected
				// as unknown for typed resources.
				`unknown field "unknownNested"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "put-default-ignore-validation",
			opts: metav1.UpdateOptions{
				FieldValidation: "Ignore",
			},
			putBodyBase: invalidBodyJSON,
		},
		{
			name:        "put-ignore-validation",
			putBodyBase: invalidBodyJSON,
			strictDecodingWarnings: []string{
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
				`duplicate field "paused"`,
				// note: fields that are both unknown
				// and duplicated will only be detected
				// as unknown for typed resources.
				`unknown field "unknownNested"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "put-strict-validation-yaml",
			opts: metav1.UpdateOptions{
				FieldValidation: "Strict",
			},
			putBodyBase: invalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingErrors: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "paused" already set in map`,
				`line 26: key "imagePullPolicy" already set in map`,
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
				`unknown field "unknownNested"`,
			},
		},
		{
			name: "put-warn-validation-yaml",
			opts: metav1.UpdateOptions{
				FieldValidation: "Warn",
			},
			putBodyBase: invalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "paused" already set in map`,
				`line 26: key "imagePullPolicy" already set in map`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
			},
		},
		{
			name: "put-ignore-validation-yaml",
			opts: metav1.UpdateOptions{
				FieldValidation: "Ignore",
			},
			putBodyBase: invalidBodyYAML,
			contentType: "application/yaml",
		},
		{
			name:        "put-no-validation-yaml",
			putBodyBase: invalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "paused" already set in map`,
				`line 26: key "imagePullPolicy" already set in map`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			putBody := []byte(fmt.Sprintf(string(tc.putBodyBase), deployName))
			req := client.CoreV1().RESTClient().Put().
				AbsPath("/apis/apps/v1").
				Namespace("default").
				Resource("deployments").
				SetHeader("Content-Type", tc.contentType).
				Name(deployName).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body([]byte(putBody)).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected request err: %v", result.Error())
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}
			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationPatchTyped tests merge-patch and json-patch requests containing unknown fields with
// strict and non-strict field validation for typed objects.
func TestFieldValidationPatchTyped(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()

	_, client, closeFn := setup(t)
	defer closeFn()

	deployName := "test-deployment"
	postBody := []byte(fmt.Sprintf(string(validBodyJSON), deployName))

	if _, err := client.CoreV1().RESTClient().Post().
		AbsPath("/apis/apps/v1").
		Namespace("default").
		Resource("deployments").
		Body(postBody).
		DoRaw(context.TODO()); err != nil {
		t.Fatalf("failed to create initial deployment: %v", err)
	}

	mergePatchBody := `
{
	"spec": {
		"unknown1": "val1",
		"unknownDupe": "valDupe",
		"unknownDupe": "valDupe2",
		"paused": true,
		"paused": false,
		"template": {
			"spec": {
				"containers": [{
					"name": "nginx",
					"image": "nginx:latest",
					"unknownNested": "val1",
					"imagePullPolicy": "Always",
					"imagePullPolicy": "Never"
				}]
			}
		}
	}
}
	`
	jsonPatchBody := `
			[
				{"op": "add", "path": "/spec/unknown1", "value": "val1", "foo":"bar"},
				{"op": "add", "path": "/spec/unknown2", "path": "/spec/unknown3", "value": "val1"},
				{"op": "add", "path": "/spec/unknownDupe", "value": "valDupe"},
				{"op": "add", "path": "/spec/unknownDupe", "value": "valDupe2"},
				{"op": "add", "path": "/spec/paused", "value": true},
				{"op": "add", "path": "/spec/paused", "value": false},
				{"op": "add", "path": "/spec/template/spec/containers/0/unknownNested", "value": "val1"},
				{"op": "add", "path": "/spec/template/spec/containers/0/imagePullPolicy", "value": "Always"},
				{"op": "add", "path": "/spec/template/spec/containers/0/imagePullPolicy", "value": "Never"}
			]
			`
	// non-conflicting mergePatch has issues with the patch (duplicate fields),
	// but doesn't conflict with the existing object it's being patched to
	nonconflictingMergePatchBody := `
{
	"spec": {
		"paused": true,
		"paused": false,
		"template": {
			"spec": {
				"containers": [{
					"name": "nginx",
					"image": "nginx:latest",
					"imagePullPolicy": "Always",
					"imagePullPolicy": "Never"
				}]
			}
		}
	}
}
			`
	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		patchType              types.PatchType
		body                   string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "merge-patch-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			patchType: types.MergePatchType,
			body:      mergePatchBody,
			strictDecodingErrors: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
			},
		},
		{
			name: "merge-patch-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			patchType: types.MergePatchType,
			body:      mergePatchBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
			},
		},
		{
			name: "merge-patch-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			patchType: types.MergePatchType,
			body:      mergePatchBody,
		},
		{
			name:      "merge-patch-no-validation",
			patchType: types.MergePatchType,
			body:      mergePatchBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknownDupe"`,
			},
		},
		{
			name:      "json-patch-strict-validation",
			patchType: types.JSONPatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body: jsonPatchBody,
			strictDecodingErrors: []string{
				// note: duplicate fields in the patch itself
				// are dropped by the
				// evanphx/json-patch library and is expected.
				// Duplicate fields in the json patch ops
				// themselves can be detected though
				`json patch unknown field "foo"`,
				`json patch duplicate field "path"`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknown3"`,
				`unknown field "unknownDupe"`,
			},
		},
		{
			name:      "json-patch-warn-validation",
			patchType: types.JSONPatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body: jsonPatchBody,
			strictDecodingWarnings: []string{
				// note: duplicate fields in the patch itself
				// are dropped by the
				// evanphx/json-patch library and is expected.
				// Duplicate fields in the json patch ops
				// themselves can be detected though
				`json patch unknown field "foo"`,
				`json patch duplicate field "path"`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknown3"`,
				`unknown field "unknownDupe"`,
			},
		},
		{
			name:      "json-patch-ignore-validation",
			patchType: types.JSONPatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body: jsonPatchBody,
		},
		{
			name:      "json-patch-no-validation",
			patchType: types.JSONPatchType,
			body:      jsonPatchBody,
			strictDecodingWarnings: []string{
				// note: duplicate fields in the patch itself
				// are dropped by the
				// evanphx/json-patch library and is expected.
				// Duplicate fields in the json patch ops
				// themselves can be detected though
				`json patch unknown field "foo"`,
				`json patch duplicate field "path"`,
				`unknown field "unknownNested"`,
				`unknown field "unknown1"`,
				`unknown field "unknown3"`,
				`unknown field "unknownDupe"`,
			},
		},
		{
			name: "nonconflicting-merge-patch-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			patchType: types.MergePatchType,
			body:      nonconflictingMergePatchBody,
			strictDecodingErrors: []string{
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "nonconflicting-merge-patch-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			patchType: types.MergePatchType,
			body:      nonconflictingMergePatchBody,
			strictDecodingWarnings: []string{
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "nonconflicting-merge-patch-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			patchType: types.MergePatchType,
			body:      nonconflictingMergePatchBody,
		},
		{
			name:      "nonconflicting-merge-patch-no-validation",
			patchType: types.MergePatchType,
			body:      nonconflictingMergePatchBody,
			strictDecodingWarnings: []string{
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			req := client.CoreV1().RESTClient().Patch(tc.patchType).
				AbsPath("/apis/apps/v1").
				Namespace("default").
				Resource("deployments").
				Name("test-deployment").
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body([]byte(tc.body)).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected request err: %v", result.Error())
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}
			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationSMP tests that attempting a strategic-merge-patch
// with unknown fields errors out when fieldValidation is strict,
// but succeeds when fieldValidation is ignored.
func TestFieldValidationSMP(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideApply, true)()
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()

	_, client, closeFn := setup(t)
	defer closeFn()

	smpBody := `
	{
		"spec": {
			"unknown1": "val1",
			"unknownDupe": "valDupe",
			"unknownDupe": "valDupe2",
			"paused": true,
			"paused": false,
			"selector": {
				"matchLabels": {
					"app": "nginx"
				}
			},
			"template": {
				"metadata": {
					"labels": {
						"app": "nginx"
					}
				},
				"spec": {
					"containers": [{
						"name": "nginx",
						"unknownNested": "val1",
						"imagePullPolicy": "Always",
						"imagePullPolicy": "Never"
					}]
				}
			}
		}
	}
	`
	// non-conflicting SMP has issues with the patch (duplicate fields),
	// but doesn't conflict with the existing object it's being patched to
	nonconflictingSMPBody := `
	{
		"spec": {
			"paused": true,
			"paused": false,
			"selector": {
				"matchLabels": {
					"app": "nginx"
				}
			},
			"template": {
				"metadata": {
					"labels": {
						"app": "nginx"
					}
				},
				"spec": {
					"containers": [{
						"name": "nginx",
						"imagePullPolicy": "Always",
						"imagePullPolicy": "Never"
					}]
				}
			}
		}
	}
	`

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		body                   string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "smp-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body: smpBody,
			strictDecodingErrors: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
				`unknown field "spec.template.spec.containers[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "smp-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body: smpBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
				`unknown field "spec.template.spec.containers[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "smp-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body: smpBody,
		},
		{
			name: "smp-no-validation",
			body: smpBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
				`unknown field "spec.template.spec.containers[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "nonconflicting-smp-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body: nonconflictingSMPBody,
			strictDecodingErrors: []string{
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "nonconflicting-smp-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body: nonconflictingSMPBody,
			strictDecodingWarnings: []string{
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
		{
			name: "nonconflicting-smp-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body: nonconflictingSMPBody,
		},
		{
			name: "nonconflicting-smp-no-validation",
			body: nonconflictingSMPBody,
			strictDecodingWarnings: []string{
				`duplicate field "paused"`,
				`duplicate field "imagePullPolicy"`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(validBodyJSON, tc.name))
			klog.Warningf("body: %s\n", string(body))
			_, err := client.CoreV1().RESTClient().Patch(types.ApplyPatchType).
				AbsPath("/apis/apps/v1").
				Namespace("default").
				Resource("deployments").
				Name(tc.name).
				Param("fieldManager", "apply_test").
				Body(body).
				Do(context.TODO()).
				Get()
			if err != nil {
				t.Fatalf("Failed to create object using Apply patch: %v", err)
			}

			req := client.CoreV1().RESTClient().Patch(types.StrategicMergePatchType).
				AbsPath("/apis/apps/v1").
				Namespace("default").
				Resource("deployments").
				Name(tc.name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body([]byte(tc.body)).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected patch err: %v", result.Error())
			}
			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected patch succeeded")
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}

			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationApplyCreate tests apply patch requests containing unknown fields
// on newly created objects, with strict and non-strict field validation.
func TestFieldValidationApplyCreate(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideApply, true)()

	_, client, closeFn := setup(t)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
				FieldManager:    "mgr",
			},
			strictDecodingErrors: []string{
				`key "paused" already set in map`,
				`key "imagePullPolicy" already set in map`,
			},
		},
		{
			name: "warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
				FieldManager:    "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "paused" already set in map`,
				`line 27: key "imagePullPolicy" already set in map`,
			},
		},
		{
			name: "ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
				FieldManager:    "mgr",
			},
		},
		{
			name: "no-validation",
			opts: metav1.PatchOptions{
				FieldManager: "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "paused" already set in map`,
				`line 27: key "imagePullPolicy" already set in map`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("apply-create-deployment-%s", tc.name)
			body := []byte(fmt.Sprintf(applyInvalidBody, name))
			req := client.CoreV1().RESTClient().Patch(types.ApplyPatchType).
				AbsPath("/apis/apps/v1").
				Namespace("default").
				Resource("deployments").
				Name(name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body(body).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected request err: %v", result.Error())
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}
			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationApplyUpdate tests apply patch requests containing unknown fields
// on apply requests to existing objects, with strict and non-strict field validation.
func TestFieldValidationApplyUpdate(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideApply, true)()

	_, client, closeFn := setup(t)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
				FieldManager:    "mgr",
			},
			strictDecodingErrors: []string{
				`key "paused" already set in map`,
				`key "imagePullPolicy" already set in map`,
			},
		},
		{
			name: "warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
				FieldManager:    "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "paused" already set in map`,
				`line 27: key "imagePullPolicy" already set in map`,
			},
		},
		{
			name: "ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
				FieldManager:    "mgr",
			},
		},
		{
			name: "no-validation",
			opts: metav1.PatchOptions{
				FieldManager: "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "paused" already set in map`,
				`line 27: key "imagePullPolicy" already set in map`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("apply-create-deployment-%s", tc.name)
			createBody := []byte(fmt.Sprintf(validBodyJSON, name))
			createReq := client.CoreV1().RESTClient().Patch(types.ApplyPatchType).
				AbsPath("/apis/apps/v1").
				Namespace("default").
				Resource("deployments").
				Name(name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			createResult := createReq.Body(createBody).Do(context.TODO())
			if createResult.Error() != nil {
				t.Fatalf("unexpected apply create err: %v", createResult.Error())
			}

			updateBody := []byte(fmt.Sprintf(applyInvalidBody, name))
			updateReq := client.CoreV1().RESTClient().Patch(types.ApplyPatchType).
				AbsPath("/apis/apps/v1").
				Namespace("default").
				Resource("deployments").
				Name(name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := updateReq.Body(updateBody).Do(context.TODO())
			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected apply err: %v", result.Error())
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}
			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationPostCRD tests that server-side schema validation
// works for CRD create requests for CRDs with schemas
func TestFieldValidationPostCRD(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	// setup the testerver and install the CRD
	noxuDefinition, rest, closeFn := setupCRD(t, false)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		body                   string
		contentType            string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "crd-post-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body: crdInvalidBody,
			strictDecodingErrors: []string{
				`duplicate field "hostPort"`,
				`duplicate field "knownField1"`,
				`duplicate field "unknownDupe"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-post-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body: crdInvalidBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-post-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body: crdInvalidBody,
		},
		{
			name: "crd-post-no-validation",
			body: crdInvalidBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-post-strict-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body:        crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingErrors: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-post-warn-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body:        crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-post-ignore-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body:        crdInvalidBodyYAML,
			contentType: "application/yaml",
		},
		{
			name:        "crd-post-no-validation-yaml",
			body:        crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name

			// create the CR as specified by the test case
			jsonBody := []byte(fmt.Sprintf(tc.body, apiVersion, kind, tc.name))
			req := rest.Post().
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				SetHeader("Content-Type", tc.contentType).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body([]byte(jsonBody)).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected post err: %v", result.Error())
			}

			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected post succeeded")
			}

			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}

			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationPostCRDSchemaless tests that server-side schema validation
// works for CRD create requests for CRDs that have schemas
// with x-kubernetes-preserve-unknown-field set
func TestFieldValidationPostCRDSchemaless(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	// setup the testerver and install the CRD
	noxuDefinition, rest, closeFn := setupCRD(t, true)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		body                   string
		contentType            string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "schemaless-crd-post-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body: crdInvalidBody,
			strictDecodingErrors: []string{
				`duplicate field "hostPort"`,
				`duplicate field "knownField1"`,
				`duplicate field "unknownDupe"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-post-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body: crdInvalidBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-post-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body: crdInvalidBody,
		},
		{
			name: "schemaless-crd-post-no-validation",
			body: crdInvalidBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-post-strict-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body:        crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingErrors: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-post-warn-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body:        crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-post-ignore-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body:        crdInvalidBodyYAML,
			contentType: "application/yaml",
		},
		{
			name:        "schemaless-crd-post-no-validation-yaml",
			body:        crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {

			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name

			// create the CR as specified by the test case
			jsonBody := []byte(fmt.Sprintf(tc.body, apiVersion, kind, tc.name))
			req := rest.Post().
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				SetHeader("Content-Type", tc.contentType).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body([]byte(jsonBody)).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected post err: %v", result.Error())
			}

			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected post succeeded")
			}

			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}

			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationPutCRD tests that server-side schema validation
// works for CRD update requests for CRDs with schemas.
func TestFieldValidationPutCRD(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	// setup the testerver and install the CRD
	noxuDefinition, rest, closeFn := setupCRD(t, false)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		putBody                string
		contentType            string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "crd-put-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			putBody: crdInvalidBody,
			strictDecodingErrors: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-put-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			putBody: crdInvalidBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-put-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			putBody: crdInvalidBody,
		},
		{
			name:    "crd-put-no-validation",
			putBody: crdInvalidBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-put-strict-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			putBody:     crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingErrors: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-put-warn-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			putBody:     crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name: "crd-put-ignore-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			putBody:     crdInvalidBodyYAML,
			contentType: "application/yaml",
		},
		{
			name:        "crd-put-no-validation-yaml",
			putBody:     crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name

			// create the CR as specified by the test case
			jsonPostBody := []byte(fmt.Sprintf(crdValidBody, apiVersion, kind, tc.name))
			postReq := rest.Post().
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			postResult, err := postReq.Body([]byte(jsonPostBody)).Do(context.TODO()).Raw()
			if err != nil {
				t.Fatalf("unexpeted error on CR creation: %v", err)
			}
			postUnstructured := &unstructured.Unstructured{}
			if err := postUnstructured.UnmarshalJSON(postResult); err != nil {
				t.Fatalf("unexpeted error unmarshalling created CR: %v", err)
			}

			// update the CR as specified by the test case
			putBody := []byte(fmt.Sprintf(tc.putBody, apiVersion, kind, tc.name, postUnstructured.GetResourceVersion()))
			klog.Warningf("putBody: %s\n", string(putBody))
			putReq := rest.Put().
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				SetHeader("Content-Type", tc.contentType).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := putReq.Body([]byte(putBody)).Do(context.TODO())
			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected put err: %v", result.Error())
			}
			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected patch succeeded")
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}

			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationPutCRDSchemaless tests that server-side schema validation
// works for CRD update requests for CRDs that have schemas
// with x-kubernetes-preserve-unknown-field set
func TestFieldValidationPutCRDSchemaless(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	// setup the testerver and install the CRD
	noxuDefinition, rest, closeFn := setupCRD(t, true)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		putBody                string
		contentType            string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "schemaless-crd-put-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			putBody: crdInvalidBody,
			strictDecodingErrors: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-put-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			putBody: crdInvalidBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-put-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			putBody: crdInvalidBody,
		},
		{
			name:    "schemaless-crd-put-no-validation",
			putBody: crdInvalidBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-put-strict-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			putBody:     crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingErrors: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-put-warn-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			putBody:     crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name: "schemaless-crd-put-ignore-validation-yaml",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			putBody:     crdInvalidBodyYAML,
			contentType: "application/yaml",
		},
		{
			name:        "schemaless-crd-put-no-validation-yaml",
			putBody:     crdInvalidBodyYAML,
			contentType: "application/yaml",
			strictDecodingWarnings: []string{
				`line 10: key "unknownDupe" already set in map`,
				`line 12: key "knownField1" already set in map`,
				`line 18: key "hostPort" already set in map`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name

			// create the CR as specified by the test case
			jsonPostBody := []byte(fmt.Sprintf(crdValidBody, apiVersion, kind, tc.name))
			postReq := rest.Post().
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			postResult, err := postReq.Body([]byte(jsonPostBody)).Do(context.TODO()).Raw()
			if err != nil {
				t.Fatalf("unexpeted error on CR creation: %v", err)
			}
			postUnstructured := &unstructured.Unstructured{}
			if err := postUnstructured.UnmarshalJSON(postResult); err != nil {
				t.Fatalf("unexpeted error unmarshalling created CR: %v", err)
			}

			// update the CR as specified by the test case
			putBody := []byte(fmt.Sprintf(tc.putBody, apiVersion, kind, tc.name, postUnstructured.GetResourceVersion()))
			klog.Warningf("putBody: %s\n", string(putBody))
			putReq := rest.Put().
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				SetHeader("Content-Type", tc.contentType).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := putReq.Body([]byte(putBody)).Do(context.TODO())
			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected put err: %v", result.Error())
			}
			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected patch succeeded")
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}

			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationPatchCRD tests that server-side schema validation
// works for jsonpatch and mergepatch requests
// for custom resources that have schemas.
func TestFieldValidationPatchCRD(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	// setup the testerver and install the CRD
	noxuDefinition, rest, closeFn := setupCRD(t, false)
	defer closeFn()

	patchYAMLBody := `
apiVersion: %s
kind: %s
metadata:
  name: %s
  finalizers:
  - test-finalizer
spec:
  cronSpec: "* * * * */5"
  ports:
  - name: x
    containerPort: 80
    protocol: TCP`

	mergePatchBody := `
{
	"spec": {
		"unknown1": "val1",
		"unknownDupe": "valDupe",
		"unknownDupe": "valDupe2",
		"knownField1": "val1",
		"knownField1": "val2",
			"ports": [{
				"name": "portName",
				"containerPort": 8080,
				"protocol": "TCP",
				"hostPort": 8081,
				"hostPort": 8082,
				"unknownNested": "val"
			}]
	}
}
	`
	jsonPatchBody := `
			[
				{"op": "add", "path": "/spec/unknown1", "value": "val1", "foo": "bar"},
				{"op": "add", "path": "/spec/unknown2", "path": "/spec/unknown3", "value": "val2"},
				{"op": "add", "path": "/spec/unknownDupe", "value": "valDupe"},
				{"op": "add", "path": "/spec/unknownDupe", "value": "valDupe2"},
				{"op": "add", "path": "/spec/knownField1", "value": "val1"},
				{"op": "add", "path": "/spec/knownField1", "value": "val2"},
				{"op": "add", "path": "/spec/ports/0/name", "value": "portName"},
				{"op": "add", "path": "/spec/ports/0/containerPort", "value": 8080},
				{"op": "add", "path": "/spec/ports/0/protocol", "value": "TCP"},
				{"op": "add", "path": "/spec/ports/0/hostPort", "value": 8081},
				{"op": "add", "path": "/spec/ports/0/hostPort", "value": 8082},
				{"op": "add", "path": "/spec/ports/0/unknownNested", "value": "val"}
			]
			`
	var testcases = []struct {
		name                   string
		patchType              types.PatchType
		opts                   metav1.PatchOptions
		body                   string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name:      "crd-merge-patch-strict-validation",
			patchType: types.MergePatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body: mergePatchBody,
			strictDecodingErrors: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name:      "crd-merge-patch-warn-validation",
			patchType: types.MergePatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body: mergePatchBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name:      "crd-merge-patch-ignore-validation",
			patchType: types.MergePatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body: mergePatchBody,
		},
		{
			name:      "crd-merge-patch-no-validation",
			patchType: types.MergePatchType,
			body:      mergePatchBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name:      "crd-json-patch-strict-validation",
			patchType: types.JSONPatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body: jsonPatchBody,
			strictDecodingErrors: []string{
				// note: duplicate fields in the patch itself
				// are dropped by the
				// evanphx/json-patch library and is expected.
				// Duplicate fields in the json patch ops
				// themselves can be detected though
				`json patch unknown field "foo"`,
				`json patch duplicate field "path"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknown3"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name:      "crd-json-patch-warn-validation",
			patchType: types.JSONPatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body: jsonPatchBody,
			strictDecodingWarnings: []string{
				// note: duplicate fields in the patch itself
				// are dropped by the
				// evanphx/json-patch library and is expected.
				// Duplicate fields in the json patch ops
				// themselves can be detected though
				`json patch unknown field "foo"`,
				`json patch duplicate field "path"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknown3"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
		{
			name:      "crd-json-patch-ignore-validation",
			patchType: types.JSONPatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body: jsonPatchBody,
		},
		{
			name:      "crd-json-patch-no-validation",
			patchType: types.JSONPatchType,
			body:      jsonPatchBody,
			strictDecodingWarnings: []string{
				// note: duplicate fields in the patch itself
				// are dropped by the
				// evanphx/json-patch library and is expected.
				// Duplicate fields in the json patch ops
				// themselves can be detected though
				`json patch unknown field "foo"`,
				`json patch duplicate field "path"`,
				`unknown field "spec.ports[0].unknownNested"`,
				`unknown field "spec.unknown1"`,
				`unknown field "spec.unknown3"`,
				`unknown field "spec.unknownDupe"`,
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name
			// create a CR
			yamlBody := []byte(fmt.Sprintf(string(patchYAMLBody), apiVersion, kind, tc.name))
			createResult, err := rest.Patch(types.ApplyPatchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				Param("fieldManager", "apply_test").
				Body(yamlBody).
				DoRaw(context.TODO())
			if err != nil {
				t.Fatalf("failed to create custom resource with apply: %v:\n%v", err, string(createResult))
			}

			// patch the CR as specified by the test case
			req := rest.Patch(tc.patchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body([]byte(tc.body)).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected patch err: %v", result.Error())
			}

			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected patch succeeded")
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}

			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationPatchCRDSchemaless tests that server-side schema validation
// works for jsonpatch and mergepatch requests
// for custom resources that have schemas
// with x-kubernetes-preserve-unknown-field set
func TestFieldValidationPatchCRDSchemaless(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	// setup the testerver and install the CRD
	noxuDefinition, rest, closeFn := setupCRD(t, true)
	defer closeFn()

	mergePatchBody := `
{
	"spec": {
		"unknown1": "val1",
		"unknownDupe": "valDupe",
		"unknownDupe": "valDupe2",
		"knownField1": "val1",
		"knownField1": "val2",
			"ports": [{
				"name": "portName",
				"containerPort": 8080,
				"protocol": "TCP",
				"hostPort": 8081,
				"hostPort": 8082,
				"unknownNested": "val"
			}]
	}
}
	`
	jsonPatchBody := `
			[
				{"op": "add", "path": "/spec/unknown1", "value": "val1", "foo": "bar"},
				{"op": "add", "path": "/spec/unknown2", "path": "/spec/unknown3", "value": "val2"},
				{"op": "add", "path": "/spec/unknownDupe", "value": "valDupe"},
				{"op": "add", "path": "/spec/unknownDupe", "value": "valDupe2"},
				{"op": "add", "path": "/spec/knownField1", "value": "val1"},
				{"op": "add", "path": "/spec/knownField1", "value": "val2"},
				{"op": "add", "path": "/spec/ports/0/name", "value": "portName"},
				{"op": "add", "path": "/spec/ports/0/containerPort", "value": 8080},
				{"op": "add", "path": "/spec/ports/0/protocol", "value": "TCP"},
				{"op": "add", "path": "/spec/ports/0/hostPort", "value": 8081},
				{"op": "add", "path": "/spec/ports/0/hostPort", "value": 8082},
				{"op": "add", "path": "/spec/ports/0/unknownNested", "value": "val"}
			]
			`
	var testcases = []struct {
		name                   string
		patchType              types.PatchType
		opts                   metav1.PatchOptions
		body                   string
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name:      "schemaless-crd-merge-patch-strict-validation",
			patchType: types.MergePatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body: mergePatchBody,
			strictDecodingErrors: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name:      "schemaless-crd-merge-patch-warn-validation",
			patchType: types.MergePatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body: mergePatchBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name:      "schemaless-crd-merge-patch-ignore-validation",
			patchType: types.MergePatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body: mergePatchBody,
		},
		{
			name:      "schemaless-crd-merge-patch-no-validation",
			patchType: types.MergePatchType,
			body:      mergePatchBody,
			strictDecodingWarnings: []string{
				`duplicate field "unknownDupe"`,
				`duplicate field "knownField1"`,
				`duplicate field "hostPort"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name:      "schemaless-crd-json-patch-strict-validation",
			patchType: types.JSONPatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
			},
			body: jsonPatchBody,
			strictDecodingErrors: []string{
				// note: duplicate fields in the patch itself
				// are dropped by the
				// evanphx/json-patch library and is expected.
				// Duplicate fields in the json patch ops
				// themselves can be detected though
				`json patch unknown field "foo"`,
				`json patch duplicate field "path"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name:      "schemaless-crd-json-patch-warn-validation",
			patchType: types.JSONPatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
			},
			body: jsonPatchBody,
			strictDecodingWarnings: []string{
				// note: duplicate fields in the patch itself
				// are dropped by the
				// evanphx/json-patch library and is expected.
				// Duplicate fields in the json patch ops
				// themselves can be detected though
				`json patch unknown field "foo"`,
				`json patch duplicate field "path"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
		{
			name:      "schemaless-crd-json-patch-ignore-validation",
			patchType: types.JSONPatchType,
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
			},
			body: jsonPatchBody,
		},
		{
			name:      "schemaless-crd-json-patch-no-validation",
			patchType: types.JSONPatchType,
			body:      jsonPatchBody,
			strictDecodingWarnings: []string{
				// note: duplicate fields in the patch itself
				// are dropped by the
				// evanphx/json-patch library and is expected.
				// Duplicate fields in the json patch ops
				// themselves can be detected though
				`json patch unknown field "foo"`,
				`json patch duplicate field "path"`,
				`unknown field "spec.ports[0].unknownNested"`,
			},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name
			// create a CR
			yamlBody := []byte(fmt.Sprintf(string(patchYAMLBody), apiVersion, kind, tc.name))
			createResult, err := rest.Patch(types.ApplyPatchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				Param("fieldManager", "apply_test").
				Body(yamlBody).
				DoRaw(context.TODO())
			if err != nil {
				t.Fatalf("failed to create custom resource with apply: %v:\n%v", err, string(createResult))
			}

			// patch the CR as specified by the test case
			req := rest.Patch(tc.patchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body([]byte(tc.body)).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected patch err: %v", result.Error())
			}

			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected patch succeeded")
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}

			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationApplyCreateCRD tests apply patch requests containing duplicate fields
// on newly created objects, for CRDs that have schemas
// Note that even prior to server-side validation, unknown fields were treated as
// errors in apply-patch and are not tested here.
func TestFieldValidationApplyCreateCRD(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideApply, true)()
	noxuDefinition, rest, closeFn := setupCRD(t, false)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
				FieldManager:    "mgr",
			},
			strictDecodingErrors: []string{
				`key "knownField1" already set in map`,
				`key "hostPort" already set in map`,
			},
		},
		{
			name: "warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
				FieldManager:    "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "knownField1" already set in map`,
				`line 16: key "hostPort" already set in map`,
			},
		},
		{
			name: "ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
				FieldManager:    "mgr",
			},
		},
		{
			name: "no-validation",
			opts: metav1.PatchOptions{
				FieldManager: "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "knownField1" already set in map`,
				`line 16: key "hostPort" already set in map`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name

			// create the CR as specified by the test case
			applyCreateBody := []byte(fmt.Sprintf(crdApplyInvalidBody, apiVersion, kind, tc.name))

			req := rest.Patch(types.ApplyPatchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body(applyCreateBody).Do(context.TODO())
			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected apply err: %v", result.Error())
			}
			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected apply succeeded")
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}
			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationApplyCreateCRDSchemaless tests apply patch requests containing duplicate fields
// on newly created objects, for CRDs that have schemas
// with x-kubernetes-preserve-unknown-field set
// Note that even prior to server-side validation, unknown fields were treated as
// errors in apply-patch and are not tested here.
func TestFieldValidationApplyCreateCRDSchemaless(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideApply, true)()
	noxuDefinition, rest, closeFn := setupCRD(t, true)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "schemaless-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
				FieldManager:    "mgr",
			},
			strictDecodingErrors: []string{
				`key "knownField1" already set in map`,
				`key "hostPort" already set in map`,
			},
		},
		{
			name: "schemaless-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
				FieldManager:    "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "knownField1" already set in map`,
				`line 16: key "hostPort" already set in map`,
			},
		},
		{
			name: "schemaless-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
				FieldManager:    "mgr",
			},
		},
		{
			name: "schemaless-no-validation",
			opts: metav1.PatchOptions{
				FieldManager: "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "knownField1" already set in map`,
				`line 16: key "hostPort" already set in map`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name

			// create the CR as specified by the test case
			applyCreateBody := []byte(fmt.Sprintf(crdApplyInvalidBody, apiVersion, kind, tc.name))

			req := rest.Patch(types.ApplyPatchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := req.Body(applyCreateBody).Do(context.TODO())
			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected apply err: %v", result.Error())
			}
			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected apply succeeded")
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}
			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationApplyUpdateCRD tests apply patch requests containing duplicate fields
// on existing objects, for CRDs with schemas
// Note that even prior to server-side validation, unknown fields were treated as
// errors in apply-patch and are not tested here.
func TestFieldValidationApplyUpdateCRD(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideApply, true)()
	noxuDefinition, rest, closeFn := setupCRD(t, false)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
				FieldManager:    "mgr",
			},
			strictDecodingErrors: []string{
				`key "knownField1" already set in map`,
				`key "hostPort" already set in map`,
			},
		},
		{
			name: "warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
				FieldManager:    "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "knownField1" already set in map`,
				`line 16: key "hostPort" already set in map`,
			},
		},
		{
			name: "ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
				FieldManager:    "mgr",
			},
		},
		{
			name: "no-validation",
			opts: metav1.PatchOptions{
				FieldManager: "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "knownField1" already set in map`,
				`line 16: key "hostPort" already set in map`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name

			// create the CR as specified by the test case
			applyCreateBody := []byte(fmt.Sprintf(crdApplyValidBody, apiVersion, kind, tc.name))
			createReq := rest.Patch(types.ApplyPatchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			createResult := createReq.Body(applyCreateBody).Do(context.TODO())
			if createResult.Error() != nil {
				t.Fatalf("unexpected apply create err: %v", createResult.Error())
			}

			applyUpdateBody := []byte(fmt.Sprintf(crdApplyInvalidBody, apiVersion, kind, tc.name))
			updateReq := rest.Patch(types.ApplyPatchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := updateReq.Body(applyUpdateBody).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected apply err: %v", result.Error())
			}
			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected apply succeeded")
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}
			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

// TestFieldValidationApplyUpdateCRDSchemaless tests apply patch requests containing duplicate fields
// on existing objects, for CRDs with schemas
// with x-kubernetes-preserve-unknown-field set
// Note that even prior to server-side validation, unknown fields were treated as
// errors in apply-patch and are not tested here.
func TestFieldValidationApplyUpdateCRDSchemaless(t *testing.T) {
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideFieldValidation, true)()
	defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ServerSideApply, true)()
	noxuDefinition, rest, closeFn := setupCRD(t, true)
	defer closeFn()

	var testcases = []struct {
		name                   string
		opts                   metav1.PatchOptions
		strictDecodingErrors   []string
		strictDecodingWarnings []string
	}{
		{
			name: "schemaless-strict-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Strict",
				FieldManager:    "mgr",
			},
			strictDecodingErrors: []string{
				`key "knownField1" already set in map`,
				`key "hostPort" already set in map`,
			},
		},
		{
			name: "schemaless-warn-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Warn",
				FieldManager:    "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "knownField1" already set in map`,
				`line 16: key "hostPort" already set in map`,
			},
		},
		{
			name: "schemaless-ignore-validation",
			opts: metav1.PatchOptions{
				FieldValidation: "Ignore",
				FieldManager:    "mgr",
			},
		},
		{
			name: "schemaless-no-validation",
			opts: metav1.PatchOptions{
				FieldManager: "mgr",
			},
			strictDecodingWarnings: []string{
				`line 10: key "knownField1" already set in map`,
				`line 16: key "hostPort" already set in map`,
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			kind := noxuDefinition.Spec.Names.Kind
			apiVersion := noxuDefinition.Spec.Group + "/" + noxuDefinition.Spec.Versions[0].Name

			// create the CR as specified by the test case
			applyCreateBody := []byte(fmt.Sprintf(crdApplyValidBody, apiVersion, kind, tc.name))
			createReq := rest.Patch(types.ApplyPatchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			createResult := createReq.Body(applyCreateBody).Do(context.TODO())
			if createResult.Error() != nil {
				t.Fatalf("unexpected apply create err: %v", createResult.Error())
			}

			applyUpdateBody := []byte(fmt.Sprintf(crdApplyInvalidBody, apiVersion, kind, tc.name))
			updateReq := rest.Patch(types.ApplyPatchType).
				AbsPath("/apis", noxuDefinition.Spec.Group, noxuDefinition.Spec.Versions[0].Name, noxuDefinition.Spec.Names.Plural).
				Name(tc.name).
				VersionedParams(&tc.opts, metav1.ParameterCodec)
			result := updateReq.Body(applyUpdateBody).Do(context.TODO())

			if result.Error() != nil && len(tc.strictDecodingErrors) == 0 {
				t.Fatalf("unexpected apply err: %v", result.Error())
			}
			if result.Error() == nil && len(tc.strictDecodingErrors) > 0 {
				t.Fatalf("unexpected apply succeeded")
			}
			for _, strictErr := range tc.strictDecodingErrors {
				if !strings.Contains(result.Error().Error(), strictErr) {
					t.Fatalf("missing strict decoding error: %s from error: %s", strictErr, result.Error().Error())
				}
			}

			if len(result.Warnings()) != len(tc.strictDecodingWarnings) {
				t.Fatalf("unexpected number of warnings, expected: %d, got: %d", len(tc.strictDecodingWarnings), len(result.Warnings()))
			}
			for i, strictWarn := range tc.strictDecodingWarnings {
				if strictWarn != result.Warnings()[i].Text {
					t.Fatalf("expected warning: %s, got warning: %s", strictWarn, result.Warnings()[i].Text)
				}

			}
		})
	}
}

func setupCRD(t *testing.T, schemaless bool) (*apiextensionsv1.CustomResourceDefinition, rest.Interface, kubeapiservertesting.TearDownFunc) {

	server, err := kubeapiservertesting.StartTestServer(t, kubeapiservertesting.NewDefaultTestServerOptions(), nil, framework.SharedEtcd())
	if err != nil {
		t.Fatal(err)
	}
	config := server.ClientConfig

	apiExtensionClient, err := apiextensionsclient.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatal(err)
	}

	preserveUnknownFields := ""
	if schemaless {
		preserveUnknownFields = `"x-kubernetes-preserve-unknown-fields": true,`
	}
	crdSchema := fmt.Sprintf(crdSchemaBase, preserveUnknownFields)

	// create the CRD
	noxuDefinition := fixtures.NewNoxuV1CustomResourceDefinition(apiextensionsv1.ClusterScoped)
	var c apiextensionsv1.CustomResourceValidation
	err = json.Unmarshal([]byte(crdSchema), &c)
	if err != nil {
		t.Fatal(err)
	}
	//noxuDefinition.Spec.PreserveUnknownFields = false
	for i := range noxuDefinition.Spec.Versions {
		noxuDefinition.Spec.Versions[i].Schema = &c
	}
	// install the CRD
	noxuDefinition, err = fixtures.CreateNewV1CustomResourceDefinition(noxuDefinition, apiExtensionClient, dynamicClient)
	if err != nil {
		t.Fatal(err)
	}

	rest := apiExtensionClient.Discovery().RESTClient()
	return noxuDefinition, rest, server.TearDownFn
}
