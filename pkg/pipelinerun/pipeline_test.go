package pipelinerun

import (
	"fmt"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

func TestParse(t *testing.T) {
	parseTests := []struct {
		filename string
		want     *PipelineRun
	}{
		{
			"testdata/example.yaml",
			&PipelineRun{
				Expression: "action == 'opened'",
				ParamBindings: []ParamBinding{
					ParamBinding{Name: "COMMIT_SHA", Value: "hook.sha"},
				},
				PipelineRunSpec: pipelinev1.PipelineRunSpec{
					PipelineSpec: &pipelinev1.PipelineSpec{
						Params: []pipelinev1.ParamSpec{
							pipelinev1.ParamSpec{
								Name:        "COMMIT_SHA",
								Type:        "string",
								Description: "the SHA for the pull_request",
							},
						},
						Tasks: []pipelinev1.PipelineTask{
							pipelinev1.PipelineTask{
								Name: "echo-commit-sha",
								TaskSpec: &pipelinev1.TaskSpec{
									Steps: []pipelinev1.Step{
										{
											Container: corev1.Container{Name: "echo", Image: "ubuntu"},
											Script:    "#!/usr/bin/env bash\necho \"$(params.COMMIT_SHA)\"\n",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range parseTests {
		t.Run(fmt.Sprintf("parsing %s", tt.filename), func(rt *testing.T) {
			f, err := os.Open(tt.filename)
			if err != nil {
				rt.Errorf("failed to open %v: %s", tt.filename, err)
			}
			defer f.Close()

			got, err := Parse(f)
			if err != nil {
				rt.Errorf("failed to parse %v: %s", tt.filename, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				rt.Errorf("Parse(%s) failed diff\n%s", tt.filename, diff)
			}
		})
	}
}
