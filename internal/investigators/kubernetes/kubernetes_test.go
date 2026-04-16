package kubernetes

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclient "k8s.io/client-go/kubernetes/fake"

	"github.com/trialanderror-eng/lolo/internal/incident"
)

func TestInvestigate_warningEventsAndUnhealthyPods(t *testing.T) {
	triggered := time.Date(2026, 4, 16, 17, 30, 0, 0, time.UTC)
	inWindow := triggered.Add(-5 * time.Minute)
	tooOld := triggered.Add(-2 * time.Hour)

	cs := fakeclient.NewSimpleClientset(
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "evt-oom", Namespace: "prod"},
			Type:           corev1.EventTypeWarning,
			Reason:         "OOMKilling",
			Message:        "container OOMKilled",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "api-1"},
			LastTimestamp:  metav1.Time{Time: inWindow},
			Count:          3,
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "evt-old", Namespace: "prod"},
			Type:           corev1.EventTypeWarning,
			Reason:         "BackOff",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "api-2"},
			LastTimestamp:  metav1.Time{Time: tooOld},
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "evt-normal", Namespace: "prod"},
			Type:           corev1.EventTypeNormal,
			Reason:         "Pulled",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "api-1"},
			LastTimestamp:  metav1.Time{Time: inWindow},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "crashing", Namespace: "prod"},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:         "app",
					RestartCount: 14,
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
						Reason: "CrashLoopBackOff", Message: "back-off 5m0s restarting failed container",
					}},
				}},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "healthy", Namespace: "prod"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
		// Pod in a namespace we won't ask about — should be ignored.
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "off-scope", Namespace: "kube-system"},
			Status:     corev1.PodStatus{Phase: corev1.PodFailed},
		},
	)

	inv := NewWithClient(cs, nil)
	inc := incident.Incident{
		ID:          "i-1",
		TriggeredAt: triggered,
		Window:      30 * time.Minute,
		Scope:       incident.Scope{Namespaces: []string{"prod"}},
	}
	ev, err := inv.Investigate(context.Background(), inc)
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	// Expect: 1 warning event in window (OOM) + 1 CrashLoopBackOff pod = 2.
	// Out-of-window event filtered, normal-type event filtered, healthy pod filtered, off-scope namespace filtered.
	if len(ev) != 2 {
		t.Fatalf("evidence count = %d, want 2: %+v", len(ev), ev)
	}
	kinds := map[string]bool{}
	for _, e := range ev {
		kinds[e.Kind] = true
	}
	if !kinds["warning_event"] || !kinds["crashloopbackoff"] {
		t.Errorf("kinds = %v, want warning_event + crashloopbackoff", kinds)
	}
	for _, e := range ev {
		if e.Kind == "warning_event" && e.Confidence != 0.85 {
			t.Errorf("OOM event confidence = %v, want 0.85 boost", e.Confidence)
		}
		if e.Kind == "crashloopbackoff" && e.Confidence != 0.9 {
			t.Errorf("CrashLoop confidence = %v, want 0.9", e.Confidence)
		}
	}
}

func TestInvestigate_noNamespacesIsNoOp(t *testing.T) {
	inv := NewWithClient(fakeclient.NewSimpleClientset(), nil)
	ev, err := inv.Investigate(context.Background(), incident.Incident{Window: 30 * time.Minute, TriggeredAt: time.Now()})
	if err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	if ev != nil {
		t.Errorf("evidence = %v, want nil when no namespaces in scope or defaults", ev)
	}
}

func TestNsSet_unionsAndDedupes(t *testing.T) {
	got := nsSet([]string{"prod", "infra"}, []string{"prod", "staging"})
	want := []string{"prod", "infra", "staging"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%s want %s", i, got[i], want[i])
		}
	}
}
