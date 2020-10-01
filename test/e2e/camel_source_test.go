// +build e2e

/*
Copyright 2020 The Knative Authors

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

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	camelv1 "github.com/apache/camel-k/pkg/apis/camel/v1"
	camelclientset "github.com/apache/camel-k/pkg/client/camel/clientset/versioned"

	"github.com/cloudevents/sdk-go/v2/test"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	testlib "knative.dev/eventing/test/lib"
	"knative.dev/eventing/test/lib/recordevents"
	"knative.dev/eventing/test/lib/resources"
	knativeduck "knative.dev/pkg/apis/duck/v1beta1"

	"knative.dev/eventing-camel/pkg/apis/sources/v1alpha1"
	camelsourceclient "knative.dev/eventing-camel/pkg/client/clientset/versioned"
)

func TestCamelSource(t *testing.T) {
	const (
		camelSourceName = "e2e-camelsource"
		loggerPodName   = "e2e-camelsource-logger-pod"
		body            = "Hello, world!"
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := testlib.Setup(t, true)
	defer testlib.TearDown(client)

	t.Logf("Creating event record")
	eventTracker, _ := recordevents.StartEventRecordOrFail(ctx, client, loggerPodName)

	camelClient := getCamelKClient(client)

	t.Logf("Creating Camel K IntegrationPlatform")
	createCamelPlatformOrFail(ctx, client, camelClient, camelSourceName)

	t.Logf("Creating CamelSource")
	createCamelSourceOrFail(ctx, client, &v1alpha1.CamelSource{
		ObjectMeta: meta.ObjectMeta{
			Name: camelSourceName,
		},
		Spec: v1alpha1.CamelSourceSpec{
			Source: v1alpha1.CamelSourceOriginSpec{
				Flow: &v1alpha1.Flow{
					"from": &map[string]interface{}{
						"uri": "timer:tick?period=1000",
						"steps": []interface{}{
							&map[string]interface{}{
								"set-body": &map[string]interface{}{
									"constant": body,
								},
							},
							&map[string]interface{}{
								"set-header": &map[string]interface{}{
									"name":     "Content-Type",
									"constant": "text/plain",
								},
							},
						},
					},
				},
			},
			Sink: &knativeduck.Destination{
				Ref: resources.ServiceRef(loggerPodName),
			},
		},
	})

	t.Logf("Waiting for all resources ready")
	client.WaitForAllTestResourcesReadyOrFail(ctx)

	t.Logf("Sleeping for 3s to let the timer tick at least once")
	time.Sleep(3 * time.Second)

	pods, err := client.Kube.Kube.CoreV1().Pods(client.Namespace).List(ctx, meta.ListOptions{
		LabelSelector: "camel.apache.org/integration",
	})
	if err != nil {
		t.Fatalf("cannot get integration pod: %v", err)
	}
	if len(pods.Items) == 0 {
		t.Fatalf("no integration pod found")
	}
	printPodLogs(ctx, t, client, pods.Items[0].Name, "integration")

	eventTracker.AssertAtLeast(1, recordevents.MatchEvent(test.AllOf(
		test.HasData([]byte(body))),
		test.HasType("org.apache.camel.event"),
	))
}

func printPodLogs(ctx context.Context, t *testing.T, c *testlib.Client, podName, containerName string) {
	logs, err := c.Kube.PodLogs(ctx, podName, containerName, c.Namespace)
	if err == nil {
		t.Log(string(logs))
	}
	t.Logf("End of pod %s logs", podName)
}

func createCamelSourceOrFail(ctx context.Context, c *testlib.Client, camelSource *v1alpha1.CamelSource) {
	camelSourceClientSet, err := camelsourceclient.NewForConfig(c.Config)
	if err != nil {
		c.T.Fatalf("Failed to create CamelSource client: %v", err)
	}

	cSources := camelSourceClientSet.SourcesV1alpha1().CamelSources(c.Namespace)
	if createdCamelSource, err := cSources.Create(ctx, camelSource, meta.CreateOptions{}); err != nil {
		c.T.Fatalf("Failed to create CamelSource %q: %v", camelSource.Name, err)
	} else {
		c.Tracker.AddObj(createdCamelSource)
	}
}

func createCamelPlatformOrFail(ctx context.Context, c *testlib.Client, camelClient camelclientset.Interface, camelSourceName string) {
	platform := camelv1.IntegrationPlatform{
		ObjectMeta: meta.ObjectMeta{
			Name:      "camel-k",
			Namespace: c.Namespace,
		},
		Spec: camelv1.IntegrationPlatformSpec{
			Profile: camelv1.TraitProfileKnative,
			Build: camelv1.IntegrationPlatformBuildSpec{
				Registry: camelv1.IntegrationPlatformRegistrySpec{
					Insecure: getBuildRegistryInsecure(),
					Address:  getBuildRegistry(),
				},
			},
		},
	}

	if _, err := camelClient.CamelV1().IntegrationPlatforms(c.Namespace).Create(ctx, &platform, meta.CreateOptions{}); err != nil {
		c.T.Fatalf("Failed to create IntegrationPlatform for CamelSource %q: %v", camelSourceName, err)
	}
}

func getCamelKClient(c *testlib.Client) camelclientset.Interface {
	return camelclientset.NewForConfigOrDie(c.Config)
}

func getBuildRegistry() string {
	registry := os.Getenv("CAMEL_K_REGISTRY")
	if registry == "" {
		registry = "registry:5000"
	}
	return registry
}

func getBuildRegistryInsecure() bool {
	insecure := os.Getenv("CAMEL_K_REGISTRY_INSECURE")
	if insecure == "" {
		insecure = "true"
	}
	return strings.ToLower(insecure) == "true"
}
