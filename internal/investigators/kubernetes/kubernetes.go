// Package kubernetes is an Investigator that surfaces what's broken in the
// cluster right now: Warning events in the incident window and pods not in
// a Running state.
package kubernetes

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/trialanderror-eng/lolo/internal/evidence"
	"github.com/trialanderror-eng/lolo/internal/incident"
)

type Investigator struct {
	client            kubernetes.Interface
	defaultNamespaces []string
	skipReason        string
}

// New constructs the investigator using in-cluster auth, falling back to
// KUBECONFIG / ~/.kube/config. defaultNamespaces is used when the incident
// itself doesn't name any. With no auth available the investigator no-ops.
func New(defaultNamespaces []string) *Investigator {
	inv := &Investigator{defaultNamespaces: defaultNamespaces}
	cfg, err := loadConfig()
	if err != nil {
		inv.skipReason = "no kubeconfig: " + err.Error()
		return inv
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		inv.skipReason = "kubernetes client: " + err.Error()
		return inv
	}
	inv.client = cs
	return inv
}

// NewWithClient is for tests: inject a fake clientset directly.
func NewWithClient(c kubernetes.Interface, defaultNamespaces []string) *Investigator {
	return &Investigator{client: c, defaultNamespaces: defaultNamespaces}
}

func loadConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
}

func (*Investigator) Name() string { return "kubernetes" }

func (i *Investigator) Investigate(ctx context.Context, inc incident.Incident) ([]evidence.Evidence, error) {
	if i.skipReason != "" {
		log.Printf("kubernetes: skipping — %s", i.skipReason)
		return nil, nil
	}
	namespaces := nsSet(i.defaultNamespaces, inc.Scope.Namespaces)
	if len(namespaces) == 0 {
		log.Printf("kubernetes: no namespaces in scope; skipping")
		return nil, nil
	}

	var out []evidence.Evidence
	for _, ns := range namespaces {
		ev, err := i.investigateNamespace(ctx, inc, ns)
		if err != nil {
			log.Printf("kubernetes: %s: %v", ns, err)
			continue
		}
		out = append(out, ev...)
	}
	return out, nil
}

func (i *Investigator) investigateNamespace(ctx context.Context, inc incident.Incident, ns string) ([]evidence.Evidence, error) {
	var out []evidence.Evidence

	events, err := i.client.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	for _, e := range events.Items {
		if e.Type != corev1.EventTypeWarning {
			continue
		}
		t := eventTime(e)
		if !inWindow(t, inc) {
			continue
		}
		out = append(out, evidence.Evidence{
			Source:     "kubernetes",
			Kind:       "warning_event",
			At:         t,
			Confidence: eventConfidence(e, inc),
			Summary:    fmt.Sprintf("%s/%s: %s — %s", ns, e.InvolvedObject.Name, e.Reason, e.Message),
			Data: map[string]any{
				"namespace": ns,
				"object":    fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name),
				"reason":    e.Reason,
				"count":     e.Count,
			},
		})
	}

	pods, err := i.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out, fmt.Errorf("list pods: %w", err)
	}
	for _, p := range pods.Items {
		if ev, ok := podEvidence(ns, p); ok {
			out = append(out, ev)
		}
	}
	return out, nil
}

// podEvidence reports a single pod if it is currently unhealthy. Returns
// false for healthy/Running pods.
func podEvidence(ns string, p corev1.Pod) (evidence.Evidence, bool) {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return evidence.Evidence{
				Source:     "kubernetes",
				Kind:       "crashloopbackoff",
				At:         time.Now().UTC(),
				Confidence: 0.9,
				Summary:    fmt.Sprintf("%s/%s container %s is CrashLoopBackOff (%d restarts)", ns, p.Name, cs.Name, cs.RestartCount),
				Data: map[string]any{
					"namespace": ns,
					"pod":       p.Name,
					"container": cs.Name,
					"restarts":  cs.RestartCount,
					"message":   cs.State.Waiting.Message,
				},
			}, true
		}
	}
	if p.Status.Phase == corev1.PodPending || p.Status.Phase == corev1.PodFailed {
		return evidence.Evidence{
			Source:     "kubernetes",
			Kind:       "pod_unhealthy",
			At:         time.Now().UTC(),
			Confidence: 0.6,
			Summary:    fmt.Sprintf("%s/%s phase=%s reason=%s", ns, p.Name, p.Status.Phase, p.Status.Reason),
			Data: map[string]any{
				"namespace": ns,
				"pod":       p.Name,
				"phase":     string(p.Status.Phase),
				"reason":    p.Status.Reason,
			},
		}, true
	}
	return evidence.Evidence{}, false
}

func eventTime(e corev1.Event) time.Time {
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	return e.FirstTimestamp.Time
}

// eventConfidence boosts events with reasons that strongly indicate root
// cause (OOM, FailedMount, etc) and otherwise weights by recency.
func eventConfidence(e corev1.Event, inc incident.Incident) float64 {
	switch e.Reason {
	case "OOMKilling", "OOMKilled", "FailedMount", "FailedAttachVolume":
		return 0.85
	case "BackOff", "Failed", "Unhealthy":
		return 0.7
	}
	gap := inc.End().Sub(eventTime(e))
	switch {
	case gap < 15*time.Minute:
		return 0.7
	case gap < 30*time.Minute:
		return 0.55
	default:
		return 0.4
	}
}

func inWindow(t time.Time, inc incident.Incident) bool {
	if t.IsZero() {
		return false
	}
	return !t.Before(inc.Start()) && !t.After(inc.End())
}

func nsSet(defaults, fromScope []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, src := range [][]string{defaults, fromScope} {
		for _, ns := range src {
			ns = strings.TrimSpace(ns)
			if ns == "" || seen[ns] {
				continue
			}
			seen[ns] = true
			out = append(out, ns)
		}
	}
	return out
}
