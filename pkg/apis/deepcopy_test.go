package apis_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/utils/ptr"

	"github.com/yokecd/yoke/pkg/apis"
	"github.com/yokecd/yoke/pkg/apis/v1alpha1"
)

func TestDeepCopy(t *testing.T) {
	t.Run("airway", func(t *testing.T) {
		var a v1alpha1.Airway
		b := apis.DeepCopy(a)
		require.EqualValues(t, a, b)
	})

	t.Run("map", func(t *testing.T) {
		original := map[string]*int{
			"a": ptr.To(1),
			"b": ptr.To(2),
		}

		copy := apis.DeepCopy(original)

		require.EqualValues(t, len(original), len(copy))
		for key := range original {
			require.Equal(t, original[key], copy[key])
			require.NotSame(t, original[key], copy[key])
		}

		original["c"] = ptr.To(3)
		require.Len(t, original, 3)
		require.Len(t, copy, 2)
	})

	t.Run("slice", func(t *testing.T) {
		original := []int{1, 2, 3}
		copy := apis.DeepCopy(original)

		require.Equal(t, len(original), len(copy))

		for i := range original {
			require.NotSame(t, &original[i], &copy[i])
			require.Equal(t, original[i], copy[i])
		}
	})

	t.Run("array", func(t *testing.T) {
		original := [3]int{1, 2, 3}
		copy := apis.DeepCopy(original)

		require.Equal(t, len(original), len(copy))

		for i := range original {
			require.NotSame(t, &original[i], &copy[i])
			require.Equal(t, original[i], copy[i])
		}
	})

	t.Run("struct", func(t *testing.T) {
		original := struct {
			A *int
		}{
			A: ptr.To(1),
		}

		copy := apis.DeepCopy(original)

		require.Equal(t, original, copy)
		require.NotSame(t, original.A, copy.A)
	})

	t.Run("pointers", func(t *testing.T) {
		x := ptr.To(420)
		y := apis.DeepCopy(x)
		require.NotSame(t, x, y)
		require.Equal(t, *x, *y)
	})
}
