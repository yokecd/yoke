package main

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yokecd/yoke/internal"
	"github.com/yokecd/yoke/internal/home"
	"github.com/yokecd/yoke/internal/k8s"
	"github.com/yokecd/yoke/pkg/yoke"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRevisionMigration(t *testing.T) {
	client, err := k8s.NewClientFromKubeConfig(home.Kubeconfig)
	require.NoError(t, err)

	secretIntf := client.Clientset.CoreV1().Secrets("default")

	now := time.Now()

	// copied blob from an example of the basic sample app. Not particularly important but at least valid.
	const rawGzipB64 = "H4sIAAAAAAAA/4yRvW4jMQyE32VqrY27UvWVV6dZuKC1Y1uw/iBpYywMvXvAJEUQ2EgqieRQ+GY0z3dI8S+szecECyml7V//wODq0wKLfywhb5GpwyCyyyJdYO8IcmRoepNSYNEklsBJC6Ot3XU9siZ2tp3P+yhJzlym4waLLV/5UKWDqTJQGmFxyvlH2ZQkshVxurDwJGvoGAba/o71RNoKnRqpLME7abB/DRoDXc9VB1G6u/x/bnjoI71K53mDvQ+DzliCdL5v/y618YXE5dTFJ9YGO2sZo+h3zLgpCgzoLprNhSHoecs1LDgY+ChntSeh+ESrDK3jcRyVLa/VUXnGOIwPG9LXz8bhLQAA///21pQuHwIAAA=="

	gzipResources, err := base64.StdEncoding.DecodeString(rawGzipB64)
	require.NoError(t, err)

	_, err = secretIntf.Create(
		background,
		&corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: "yoke.5345781044a3",
				Labels: map[string]string{
					internal.LabelRelease: "foo",
					internal.LabelKind:    "revision",
				},
				Annotations: map[string]string{
					internal.AnnotationActiveAt:       now.Format(time.RFC3339Nano),
					internal.AnnotationCreatedAt:      now.Format(time.RFC3339Nano),
					internal.AnnotationResourceCount:  "1",
					internal.AnnotationSourceChecksum: "not-relevant",
					internal.AnnotationSourceURL:      "also-not-relevant",
				},
			},
			Data: map[string][]byte{internal.KeyResources: gzipResources},
			Type: corev1.SecretTypeOpaque,
		},
		metav1.CreateOptions{},
	)
	require.NoError(t, err)

	commander := yoke.FromK8Client(client)

	require.NoError(t, commander.Takeoff(background, yoke.TakeoffParams{
		Release:   "foo",
		Namespace: "default",
		Flight: yoke.FlightParams{
			Input: strings.NewReader("[]"),
		},
	}))

	list, err := secretIntf.List(background, metav1.ListOptions{LabelSelector: "internal.yoke/release=foo,internal.yoke/kind=revision"})
	require.NoError(t, err)
	require.Len(t, list.Items, 0)

	list, err = secretIntf.List(background, metav1.ListOptions{LabelSelector: fmt.Sprintf("internal.yoke/release=%s,internal.yoke/kind=revision", internal.SHA1HexFromString("foo"))})
	require.NoError(t, err)
	require.Len(t, list.Items, 2)

	for _, item := range list.Items {
		require.Equal(t, internal.SHA1HexFromString("foo"), item.Labels[internal.LabelRelease])
		require.Equal(t, "foo", item.Annotations[internal.AnnotationReleaseName])
	}
}
