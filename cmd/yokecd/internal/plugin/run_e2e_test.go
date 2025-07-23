package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/cmd/yokecd/internal/svr"
	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/x"
)

func TestPluginE2E(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/flight.wasm ../testing/mods/flight", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	wasm, err := os.ReadFile("./test_output/flight.wasm")
	require.NoError(t, err)

	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(wasm)
	}))
	defer sourceServer.Close()

	done := make(chan struct{})
	defer func() { <-done }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		defer close(done)
		if err := svr.Run(ctx, svr.Config{}); err != nil && !errors.Is(err, context.Canceled) {
			require.FailNow(t, "unexpected error running server", err.Error())
		}
	}()

	var stdout bytes.Buffer
	ctx = internal.WithStdout(ctx, io.MultiWriter(&stdout, os.Stdout))

	require.NoError(t, Run(ctx, Config{
		Application: ArgoApp{
			Name:      "test",
			Namespace: "foo",
		},
		Flight: Parameters{
			Wasm:  sourceServer.URL,
			Input: `{"foo": {"hello":"world"}, "bar": {"potato":"farm"}}`,
		},
	}))

	var actual []corev1.ConfigMap

	decoder := json.NewDecoder(&stdout)
	for {
		var cm corev1.ConfigMap
		if err := decoder.Decode(&cm); err == io.EOF {
			break
		}
		actual = append(actual, cm)
	}

	slices.SortFunc(actual, func(a, b corev1.ConfigMap) int { return strings.Compare(a.Name, b.Name) })

	require.Equal(
		t,
		[]corev1.ConfigMap{
			{
				TypeMeta: v1.TypeMeta{Kind: "ConfigMap"},
				ObjectMeta: v1.ObjectMeta{
					Name: "bar",
					Labels: map[string]string{
						"app.kubernetes.io/managed-by":             "yokecd",
						"app.kubernetes.io/yoke-release":           "test",
						"app.kubernetes.io/yoke-release-namespace": "foo",
					},
				},
				Data: map[string]string{"potato": "farm"},
			},
			{
				TypeMeta: v1.TypeMeta{Kind: "ConfigMap"},
				ObjectMeta: v1.ObjectMeta{
					Name: "foo",
					Labels: map[string]string{
						"app.kubernetes.io/managed-by":             "yokecd",
						"app.kubernetes.io/yoke-release":           "test",
						"app.kubernetes.io/yoke-release-namespace": "foo",
					},
				},
				Data: map[string]string{"hello": "world"},
			},
		},
		actual,
	)
}
