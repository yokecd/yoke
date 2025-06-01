package flight

import (
	"reflect"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/pkg/openapi"
)

// Status is a basic status representation used for Flights by the ATC as well as for Airways.
type Status struct {
	// Conditions are the conditions that are met for this flight. Only the Ready condition is set by yoke
	// but you may set your own conditions.
	Conditions Conditions `json:"conditions,omitempty"`
}

type Conditions []metav1.Condition

func (conditions Conditions) OpenAPISchema() *apiextensionsv1.JSONSchemaProps {
	schema := openapi.SchemaFrom(reflect.TypeFor[[]metav1.Condition]())
	schema.XListType = ptr.To("map")
	schema.XListMapKeys = []string{"type"}
	return schema
}
