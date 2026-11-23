// These tests differentiate from cmd/yoke/main_test.go in that they must not require a backing kubernetes cluster.
package yoke_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/pkg/flight"
	"github.com/yokecd/yoke/pkg/yoke"
)

func TestFlightResourceValidation(t *testing.T) {
	var commander yoke.Commander

	err := commander.Takeoff(t.Context(), yoke.TakeoffParams{
		Release: "foobar",
		Flight: yoke.FlightParams{
			Input: internal.JSONReader(flight.Resources{
				&corev1.ConfigMap{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					ObjectMeta: metav1.ObjectMeta{Name: "../../.aws/credentials"},
					Data:       map[string]string{},
				},
				&corev1.ConfigMap{
					APIVersion: "v1",
					Kind:       "ConfigMap",
					ObjectMeta: metav1.ObjectMeta{Name: strings.Repeat("a", 256)},
					Data:       map[string]string{},
				},
			}),
		},
	})
	require.True(t, internal.IsValidationErr(err), "error was unexpected not a validation error", err)
	require.ErrorContains(t, err, "../../.aws/credentials: invalid .metadata.name: a lowercase RFC 1123 subdomain must")
	require.ErrorContains(t, err, "aaaaaa: invalid .metadata.name: must be no more than 253 characters")
}
