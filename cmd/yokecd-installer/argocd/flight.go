package argocd

import (
	_ "embed"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/yokecd/yoke/pkg/helm"
)

//go:embed argo-cd-8.1.2.tgz
var archive []byte

// RenderChart renders the chart downloaded from https://argoproj.github.io/argo-helm/argo-cd
// Producing version: 8.1.2
func RenderChart(release, namespace string, values map[string]any) ([]*unstructured.Unstructured, error) {
	chart, err := helm.LoadChartFromZippedArchive(archive)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart from zipped archive: %w", err)
	}

	return chart.Render(release, namespace, values)
}
