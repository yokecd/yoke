package {{ .Package }}

import (
	_ "embed"
	"fmt"

	"github.com/yokecd/yoke/pkg/helm"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

//go:embed {{ .Archive }}
var archive []byte

// RenderChart renders the chart downloaded from {{.URL}}
// Producing version: {{ .Version }}
func RenderChart(release, namespace string, values {{if .UseFallback}}map[string]any{{else}}*Values{{end}}) ([]*unstructured.Unstructured, error) {
	chart, err := helm.LoadChartFromZippedArchive(archive)
	if err != nil {
		return nil, fmt.Errorf("failed to load chart from zipped archive: %w", err)
	}

	return chart.Render(release, namespace, values)
}
