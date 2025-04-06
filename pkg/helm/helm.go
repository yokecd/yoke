package helm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"

	"github.com/davidmdm/x/xerr"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/yokecd/yoke/internal"
)

func LoadChartFromZippedArchive(data []byte) (chart *Chart, err error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer func() {
		err = xerr.MultiErrFrom("", err, gz.Close())
	}()

	archive := tar.NewReader(gz)

	var files []*loader.BufferedFile
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to iterate through archive: %w", err)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		content, err := io.ReadAll(archive)
		if err != nil {
			return nil, err
		}

		files = append(files, &loader.BufferedFile{
			Name: header.Name,
			Data: content,
		})
	}

	stripToChart(files)

	underlyingChart, err := loader.LoadFiles(files)
	if err != nil {
		return nil, err
	}

	var values []byte
	for _, f := range files {
		if name := f.Name; name == "values.yaml" || name == "values.yml" {
			values = f.Data
			break
		}
	}

	return &Chart{
		Chart:  underlyingChart,
		Values: values,
	}, nil
}

func LoadChartFromFS(fs embed.FS) (*Chart, error) {
	files, err := getAllFilesFromDir(fs, ".")
	if err != nil {
		return nil, fmt.Errorf("failed to get files from FS: %w", err)
	}

	stripToChart(files)

	underlyingChart, err := loader.LoadFiles(files)
	if err != nil {
		return nil, err
	}

	var values []byte
	for _, f := range files {
		if name := f.Name; name == "values.yaml" || name == "values.yml" {
			values = f.Data
			break
		}
	}

	return &Chart{
		Chart:  underlyingChart,
		Values: values,
	}, nil
}

type Chart struct {
	*chart.Chart
	Values []byte
}

func (chart Chart) Render(release, namespace string, values any) ([]*unstructured.Unstructured, error) {
	opts := chartutil.ReleaseOptions{
		Name:      release,
		Namespace: namespace,
	}

	capabilities := chartutil.DefaultCapabilities.Copy()

	valueMap, err := asMap(values)
	if err != nil {
		return nil, fmt.Errorf("failed to convert values to map: %w", err)
	}

	chartutil.ProcessDependencies(chart.Chart, valueMap)

	valueMap, err = chartutil.ToRenderValues(chart.Chart, valueMap, opts, capabilities)
	if err != nil {
		return nil, err
	}

	rendered, err := engine.Engine{}.Render(chart.Chart, valueMap)
	if err != nil {
		return nil, err
	}

	var results []*unstructured.Unstructured

	for name, content := range rendered {
		if ext := path.Ext(name); ext != ".yaml" && ext != ".yml" {
			continue
		}
		if strings.HasPrefix(name, "tests/") || strings.Contains(name, "/tests/") {
			continue
		}

		decoder := yaml.NewYAMLToJSONDecoder(strings.NewReader(content))

		for {
			var resource unstructured.Unstructured
			if err := decoder.Decode(&resource); err != nil {
				if err == io.EOF {
					break
				}
				return nil, fmt.Errorf("%s: %w\n%s", name, err, content)
			}
			if resource.Object == nil {
				continue
			}
			results = append(results, &resource)
		}
	}

	slices.SortFunc(results, func(a, b *unstructured.Unstructured) int {
		return strings.Compare(internal.Canonical(a), internal.Canonical(b))
	})

	return results, nil
}

func asMap(values any) (map[string]any, error) {
	if m, ok := values.(map[string]any); ok {
		return m, nil
	}

	type Mappter interface {
		ToMap() (map[string]any, error)
	}
	if v, ok := values.(Mappter); ok {
		return v.ToMap()
	}

	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}

	var m map[string]any
	err = json.Unmarshal(data, &m)
	return m, err
}

// stripToChart modifies the names of the files such that it removes the segments
// prior to the nearest Chart.yaml. This is done so that helm can recognize these files
// as a chart. Usually when they are loaded in from the filesystem or archive their exists
// a number (usually 1) folders to contain the chart. We need to strip those away.
func stripToChart(files []*loader.BufferedFile) {
	idx := -1
	for _, file := range files {
		file.Name = path.Clean(file.Name)
		if base := path.Base(file.Name); base != "Chart.yaml" && base != "Chart.yml" {
			continue
		}
		if length := len(strings.Split(file.Name, "/")); idx == -1 || length < idx {
			idx = length
		}
	}
	if idx == -1 {
		return
	}

	for _, file := range files {
		file.Name = strings.Join(strings.Split(file.Name, "/")[idx-1:], "/")
	}
}

func getAllFilesFromDir(fs embed.FS, p string) ([]*loader.BufferedFile, error) {
	entries, err := fs.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("failed to read dir at %s: %w", p, err)
	}

	var results []*loader.BufferedFile
	for _, entry := range entries {
		filepath := path.Join(p, entry.Name())
		if entry.IsDir() {
			subEntries, err := getAllFilesFromDir(fs, filepath)
			if err != nil {
				return nil, err
			}
			results = append(results, subEntries...)
			continue
		}

		content, err := fs.ReadFile(filepath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file at %s: %w", filepath, err)
		}

		results = append(results, &loader.BufferedFile{
			Name: path.Join(p, entry.Name()),
			Data: content,
		})
	}

	return results, nil
}
