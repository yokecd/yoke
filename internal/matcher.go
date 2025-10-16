package internal

import (
	"strings"

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
