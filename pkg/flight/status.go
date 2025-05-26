package flight

import (
	"encoding/json"
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
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Props are the top level fields other than conditions found in the status.
	// Since status can be extended by users in any way they see fit, we cannot statically
	// type status other than for standard Conditions.
	//
	// Props serves as a target for arbitrary json deserialization and for users to programatically access
	// status properties. Conditions are ignored from props during marshalling and unmarshalling and should be accessed via Conditions.
	Props map[string]any `json:"-"`
}

func (status Status) MarshalJSON() ([]byte, error) {
	props := status.Props
	if props == nil {
		props = map[string]any{}
	}

	props["conditions"] = status.Conditions

	return json.Marshal(props)
}

func (status *Status) UnmarshalJSON(data []byte) error {
	type StatusAlt Status
	if err := json.Unmarshal(data, (*StatusAlt)(status)); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &status.Props); err != nil {
		return err
	}

	delete(status.Props, "conditions")

	if len(status.Props) == 0 {
		status.Props = nil
	}
	return nil
}

func (status Status) OpenAPISchema() *apiextensionsv1.JSONSchemaProps {
	type StatusAlt Status
	schema := openapi.SchemaFrom(reflect.TypeFor[StatusAlt]())
	schema.XPreserveUnknownFields = ptr.To(true)
	return schema
}
