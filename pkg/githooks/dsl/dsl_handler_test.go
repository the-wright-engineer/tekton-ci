package dsl

import (
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jenkins-x/go-scm/scm"
	"github.com/jenkins-x/go-scm/scm/factory"
	fakeclientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/bigkevmcd/tekton-ci/pkg/git"
	"github.com/bigkevmcd/tekton-ci/pkg/volumes"
	"github.com/bigkevmcd/tekton-ci/test"
)

const testNS = "testing"

func TestHandlePullRequestEvent(t *testing.T) {
	as := test.MakeAPIServer(t, "/api/v3/repos/Codertocat/Hello-World/contents/.tekton_ci.yaml", "refs/pull/2/head", "testdata/content.json")
	defer as.Close()
	scmClient, err := factory.NewClient("github", as.URL, "", factory.Client(as.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gitClient := git.New(scmClient)
	fakeTektonClient := fakeclientset.NewSimpleClientset()
	fakeClient := fake.NewSimpleClientset()
	vc := volumes.New(fakeClient)
	logger := zaptest.NewLogger(t, zaptest.Level(zap.WarnLevel))
	h := New(gitClient, fakeTektonClient, vc, testNS, logger.Sugar())
	req := makeHookRequest(t, "testdata/github_pull_request.json", "pull_request")
	hook, err := gitClient.ParseWebhookRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()

	h.PullRequest(context.TODO(), hook.(*scm.PullRequestHook), rec)

	w := rec.Result()
	if w.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want %d: %s", w.StatusCode, http.StatusNotFound, mustReadBody(t, w))
	}
	claim, err := fakeClient.CoreV1().PersistentVolumeClaims(testNS).Get("", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// TODO: This should probably be a call to a function in volumes.
	wantClaim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "simple-volume-",
			Namespace:    testNS,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": defaultVolumeSize,
				},
			},
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteMany,
			},
			VolumeMode: &volumes.SimpleVolumeMode,
		},
	}

	if diff := cmp.Diff(wantClaim, claim, cmpopts.IgnoreFields(corev1.PersistentVolumeClaim{}, "TypeMeta")); diff != "" {
		t.Fatalf("persistent volume claim incorrect, diff\n%s", diff)
	}
	pr, err := fakeTektonClient.TektonV1beta1().PipelineRuns(testNS).Get("", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if l := len(pr.Spec.PipelineSpec.Tasks); l != 4 {
		t.Fatalf("got %d tasks, want 4", l)
	}
	// check that it picked up the correct source URL and branch from the
	// fixture file.
	want := []string{
		"/ko-app/git-init",
		"-url", "https://github.com/Codertocat/Hello-World.git",
		"-revision", "ec26c3e57ca3a959ca5aad62de7213c562f8c821",
		"-path", "$(workspaces.source.path)",
	}
	if diff := cmp.Diff(want, pr.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Container.Command); diff != "" {
		t.Fatalf("git command incorrect, diff\n%s", diff)
	}
}

func TestHandlePullRequestEventNoPipeline(t *testing.T) {
	as := test.MakeAPIServer(t, "/api/v3/repos/Codertocat/Hello-World/contents/.tekton_ci.yaml", "refs/pull/2/head", "")
	defer as.Close()
	scmClient, err := factory.NewClient("github", as.URL, "", factory.Client(as.Client()))
	if err != nil {
		t.Fatal(err)
	}
	gitClient := git.New(scmClient)
	fakeTektonClient := fakeclientset.NewSimpleClientset()
	fakeClient := fake.NewSimpleClientset()
	vc := volumes.New(fakeClient)
	logger := zaptest.NewLogger(t, zaptest.Level(zap.WarnLevel))
	h := New(gitClient, fakeTektonClient, vc, testNS, logger.Sugar())
	req := makeHookRequest(t, "testdata/github_pull_request.json", "pull_request")
	hook, err := gitClient.ParseWebhookRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()

	h.PullRequest(context.TODO(), hook.(*scm.PullRequestHook), rec)

	w := rec.Result()
	if w.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want %d: %s", w.StatusCode, http.StatusOK, mustReadBody(t, w))
	}
	_, err = fakeTektonClient.TektonV1beta1().PipelineRuns(testNS).Get("", metav1.GetOptions{})
	if !errors.IsNotFound(err) {
		t.Fatalf("pipelinerun was created when no pipeline definition exists")
	}
}

func serialiseToJSON(t *testing.T, e interface{}) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("failed to marshal %#v to JSON: %s", e, err)
	}
	return bytes.NewBuffer(b)
}

// TODO use uuid to generate the Delivery ID.
func makeHookRequest(t *testing.T, fixture, eventType string) *http.Request {
	req := httptest.NewRequest("POST", "/", serialiseToJSON(t, test.ReadJSONFixture(t, fixture)))
	req.Header.Add("X-GitHub-Delivery", "72d3162e-cc78-11e3-81ab-4c9367dc0958")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-GitHub-Event", eventType)
	return req
}

func mustReadBody(t *testing.T, req *http.Response) []byte {
	t.Helper()
	b, err := ioutil.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
