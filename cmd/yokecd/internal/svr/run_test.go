package svr

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/internal/wasi/cache"
	"github.com/yokecd/yoke/internal/x"
	wasik8s "github.com/yokecd/yoke/pkg/flight/wasi/k8s"
	"github.com/yokecd/yoke/pkg/yoke"
)

func TestPluginServer(t *testing.T) {
	require.NoError(t, x.X("go build -o ./test_output/echo.wasm ../testing/mods/echo", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	wasm, err := os.ReadFile("./test_output/echo.wasm")
	require.NoError(t, err)

	sourceServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(wasm)
	}))
	defer sourceServer.Close()

	sourceServer.Listener, err = net.Listen("tcp", ":6663")
	require.NoError(t, err)

	sourceServer.Start()

	var stdout bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&stdout, nil))

	mods := cache.NewModuleCache("./test_output", nil)

	modCount := func() (count int) {
		for range mods.All() {
			count++
		}
		return count
	}

	svr := httptest.NewUnstartedServer(Handler(mods, logger, nil))
	defer svr.Close()

	// Match Exec's hardcoded default.
	svr.Listener, err = net.Listen("tcp", ":3666")
	require.NoError(t, err)

	svr.Start()

	echo, err := Exec(context.Background(), ExecuteReq{
		Path:      sourceServer.URL,
		Release:   "foo",
		Namespace: "bar",
		Args:      []string{"a", "r", "g"},
		Env: map[string]string{
			"FOO": "BAR",
		},
		Input: "banana hamock",
	})
	require.NoError(t, err)

	require.JSONEq(
		t,
		`{
			"args": ["a","r","g"],
			"env": {
				"FOO":"BAR",
				"NAMESPACE":"bar",
				"YOKE_NAMESPACE":"bar",
				"YOKE_RELEASE":"foo",
				"YOKE_VERSION":"(devel)"
			 },
			"input":"banana hamock"
		}`,
		string(echo),
	)

	require.Equal(t, 1, modCount())

	_, err = Exec(context.Background(), ExecuteReq{
		Path:      sourceServer.URL,
		Release:   "baz",
		Namespace: "default",
	})
	require.NoError(t, err)

	require.Equal(t, 1, modCount())

	type Log struct {
		Elapsed metav1.Duration `json:"elapsed"`
	}

	var logs []Log
	decoder := json.NewDecoder(&stdout)
	for {
		var log Log
		if err := decoder.Decode(&log); err == io.EOF {
			break
		}
		logs = append(logs, log)

		// for debugging purposes
		_ = json.NewEncoder(t.Output()).Encode(log)
	}

	require.Len(t, logs, 2)
	require.True(t, logs[0].Elapsed.Duration > logs[1].Elapsed.Duration, "expected compile time to be greater than cache time")
}

func TestPluginServerLookup(t *testing.T) {
	require.NoError(t, x.X("kind delete cluster --name yokecd-plugin-test"))
	require.NoError(t, x.X("kind create cluster --name yokecd-plugin-test"))

	require.NoError(t, x.X("go build -o ./test_output/lookup.wasm ../testing/mods/lookup", x.Env("GOOS=wasip1", "GOARCH=wasm")))

	wasm, err := os.ReadFile("./test_output/lookup.wasm")
	require.NoError(t, err)

	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(wasm)
	}))
	defer sourceServer.Close()

	var stdout bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&stdout, nil))

	mods := cache.NewModuleCache("./test_output", nil)

	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	svr := httptest.NewUnstartedServer(Handler(mods, logger, client))
	defer svr.Close()

	listener, err := net.Listen("tcp", ":3666")
	require.NoError(t, err)

	// Match Exec's hardcoded default.
	svr.Listener = listener
	svr.Start()

	cmIntf := client.Clientset.CoreV1().ConfigMaps("default")

	cm, err := cmIntf.Create(
		t.Context(),
		&corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "ConfigMap",
			},
			ObjectMeta: metav1.ObjectMeta{Name: "test-cm"},
			Data:       map[string]string{"key": "value"},
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	identifer := wasik8s.ResourceIdentifier{
		Name:       cm.Name,
		Namespace:  cm.Namespace,
		Kind:       "ConfigMap",
		ApiVersion: "v1",
	}

	identifierBytes, err := json.Marshal(identifer)
	require.NoError(t, err)

	_, err = Exec(context.Background(), ExecuteReq{
		Path:          sourceServer.URL,
		Release:       "foo",
		Namespace:     "bar",
		ClusterAccess: yoke.ClusterAccessParams{Enabled: false},
		Input:         string(identifierBytes),
	})
	require.ErrorContains(t, err, "access to the cluster has not been granted for this flight invocation")

	_, err = Exec(context.Background(), ExecuteReq{
		Path:          sourceServer.URL,
		Release:       "foo",
		Namespace:     "bar",
		ClusterAccess: yoke.ClusterAccessParams{Enabled: true},
		Input:         string(identifierBytes),
	})
	require.ErrorContains(t, err, "forbidden: cannot access resource outside of target release ownership")

	data, err := Exec(context.Background(), ExecuteReq{
		Path:          sourceServer.URL,
		Release:       "foo",
		Namespace:     "bar",
		ClusterAccess: yoke.ClusterAccessParams{Enabled: true, ResourceMatchers: []string{"ConfigMap"}},
		Input:         string(identifierBytes),
	})
	require.NoError(t, err)

	var actual corev1.ConfigMap
	require.NoError(t, json.Unmarshal(data, &actual))

	require.Equal(t, "value", actual.Data["key"])
}
