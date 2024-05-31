package internal

import (
	"cmp"
	"crypto/sha1"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Revisions struct {
	Release     string     `json:"release"`
	Total       int        `json:"total"`
	ActiveIndex int        `json:"activeIndex"`
	History     []Revision `json:"history"`
}

type Source struct {
	Ref      string `json:"ref"`
	Checksum string `json:"checksum"`
}

func SourceFrom(ref string, wasm []byte) (src Source) {
	if len(wasm) > 0 {
		src.Checksum = fmt.Sprintf("%x", sha1.Sum(wasm))
	}

	if ref != "" {
		u, _ := url.Parse(ref)
		if u.Scheme != "" {
			src.Ref = u.String()
		} else {
			src.Ref = path.Clean(ref)
		}
	}

	return
}

func (revisions *Revisions) Add(resources []*unstructured.Unstructured, ref string, wasm []byte) {
	revisions.History = append(revisions.History, Revision{
		ID:        revisions.Total + 1,
		Source:    SourceFrom(ref, wasm),
		CreatedAt: time.Now(),
		Resources: resources,
	})
	revisions.ActiveIndex = len(revisions.History) - 1
	revisions.Total++
}

func (revisions Revisions) CurrentResources() []*unstructured.Unstructured {
	if len(revisions.History) == 0 {
		return nil
	}
	return revisions.History[revisions.ActiveIndex].Resources
}

type Revision struct {
	ID        int                          `json:"id"`
	Source    Source                       `json:"source"`
	CreatedAt time.Time                    `json:"createdAt"`
	Resources []*unstructured.Unstructured `json:"resources"`
}

func AddYokeMetadata(resources []*unstructured.Unstructured, release string) {
	for _, resource := range resources {
		labels := resource.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels["app.kubernetes.io/managed-by"] = "yoke"
		labels["app.kubernetes.io/yoke-release"] = release
		resource.SetLabels(labels)
	}
}

func Canonical(resource *unstructured.Unstructured) string {
	gvk := resource.GetObjectKind().GroupVersionKind()

	return strings.ToLower(strings.Join(
		[]string{
			Namespace(resource),
			cmp.Or(gvk.Group, "core"),
			gvk.Version,
			resource.GetKind(),
			resource.GetName(),
		},
		".",
	))
}

func Namespace(resource *unstructured.Unstructured) string {
	return cmp.Or(resource.GetNamespace(), "_")
}

func CanonicalNameList(resources []*unstructured.Unstructured) []string {
	result := make([]string, len(resources))
	for i, resource := range resources {
		result[i] = Canonical(resource)
	}
	return result
}

func CanonicalMap(resources []*unstructured.Unstructured) map[string]*unstructured.Unstructured {
	result := make(map[string]*unstructured.Unstructured, len(resources))
	for _, resource := range resources {
		result[Canonical(resource)] = resource
	}
	return result
}

func CanonicalObjectMap(resources []*unstructured.Unstructured) map[string]any {
	result := make(map[string]any, len(resources))
	for _, resource := range resources {
		result[Canonical(resource)] = resource.Object
	}
	return result
}
