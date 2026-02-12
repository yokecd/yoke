package internal

import (
	"encoding"
	"fmt"
	"net/url"
	"path"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func MatchResource(resource *unstructured.Unstructured, matcher string) bool {
	switch matcher {
	case "":
		return false
	case "*":
		return true
	}

	namespace, gk, name := parseMatcherExpr(matcher)

	if namespace != "*" && resource.GetNamespace() != namespace {
		return false
	}

	if gk != "*" && resource.GroupVersionKind().GroupKind().String() != gk {
		return false
	}

	if name != "*" && resource.GetName() != name {
		return false
	}

	return true
}

func MatcherContains(parent, child string) bool {
	parentNS, parentGK, parentName := parseMatcherExpr(parent)
	childNS, childGK, childName := parseMatcherExpr(child)
	return true &&
		(parentNS == "*" || parentNS == childNS) &&
		(parentGK == "*" || parentGK == childGK) &&
		(parentName == "*" || parentName == childName)
}

func parseMatcherExpr(matcher string) (string, string, string) {
	ns, gkn, ok := strings.Cut(matcher, "/")
	if !ok {
		gkn = ns
		ns = "*"
	}
	gk, name, ok := strings.Cut(gkn, ":")
	if !ok {
		name = "*"
	}
	return ns, gk, name
}

type URLGlobs []url.URL

var _ encoding.TextUnmarshaler = (*URLGlobs)(nil)

func (globs *URLGlobs) OpenAPISchema() *apiextensionsv1.JSONSchemaProps {
	return &apiextensionsv1.JSONSchemaProps{
		Type:  "array",
		Items: &apiextensionsv1.JSONSchemaPropsOrArray{Schema: &apiextensionsv1.JSONSchemaProps{Type: "string"}},
	}
}

func (globs *URLGlobs) UnmarshalText(data []byte) error {
	for value := range strings.SplitSeq(string(data), ",") {
		uri, err := url.Parse(value)
		if err != nil {
			return fmt.Errorf("failed to parse %q: %w", value, err)
		}
		*globs = append(*globs, *uri)
	}
	return nil
}

func (globs URLGlobs) Match(value string) (bool, error) {
	if len(globs) == 0 {
		return true, nil
	}

	target, err := url.Parse(value)
	if err != nil {
		return false, fmt.Errorf("failed to parse url: %w", err)
	}

	for _, glob := range globs {
		if target.Scheme != glob.Scheme {
			continue
		}
		if ok, _ := path.Match(path.Join(glob.Host, glob.Path), path.Join(target.Host, target.Path)); ok {
			return true, nil
		}
	}

	return false, nil
}
