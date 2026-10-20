package diagnostic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const (
	imagePullDiagnosticID      = "image-pull"
	imagePullDiagnosticVersion = "v1"
	imagePullEventRequirement  = "image-pull-event"
	imagePullCauseRequirement  = "image-pull-cause-detail"
)

// ImagePullDiagnostic classifies Kubernetes image-pull evidence. It proposes
// claims only; the existing claim verifier decides whether any causal claim
// can become a confirmed RCA conclusion.
type ImagePullDiagnostic struct{}

func (ImagePullDiagnostic) ID() string      { return imagePullDiagnosticID }
func (ImagePullDiagnostic) Version() string { return imagePullDiagnosticVersion }

func (d ImagePullDiagnostic) Match(evidence EvidenceSet) MatchResult {
	if evidence.Scope.Deployment.UID != "" && evidence.Scope.Complete && len(evidence.Scope.Pods) == 0 {
		return MatchResult{Reason: "validated Deployment scope has no Pods"}
	}
	if len(imagePullEvidenceIDs(evidence)) == 0 {
		return MatchResult{Reason: "no ErrImagePull or ImagePullBackOff evidence"}
	}
	return MatchResult{Applicable: true, Reason: "image-pull evidence is present"}
}

func (ImagePullDiagnostic) Requirements(_ EvidenceSet) []Requirement {
	return []Requirement{
		{ID: imagePullEventRequirement, Capability: domain.QueryTypeEvent, Description: "a target-scoped ErrImagePull or ImagePullBackOff Event", Required: true},
		{ID: imagePullCauseRequirement, Capability: domain.QueryTypeEvent, Description: "untruncated Event detail for a specific image-pull failure mechanism", Required: false},
	}
}

func (d ImagePullDiagnostic) Evaluate(evidence EvidenceSet) Result {
	result := Result{
		DiagnosticID:        d.ID(),
		DiagnosticVersion:   d.Version(),
		Status:              StatusInconclusive,
		Claims:              []CandidateClaim{},
		MissingRequirements: []Requirement{},
		Warnings:            []string{},
	}
	ids := imagePullEvidenceIDs(evidence)
	if len(ids) == 0 {
		result.Status = StatusNotApplicable
		result.Summary = "ImagePullDiagnostic did not find target-scoped image-pull evidence."
		result.MissingRequirements = append(result.MissingRequirements, d.Requirements(evidence)[0])
		return result
	}

	result.Claims = append(result.Claims,
		CandidateClaim{
			ID:           "image-pull-observed",
			Type:         ClaimObservation,
			Statement:    "A target container is waiting because Kubernetes reported an image-pull failure.",
			EvidenceRefs: ids,
		},
		CandidateClaim{
			ID:           "image-pull-failure",
			Type:         ClaimFailureMechanism,
			Statement:    "The target container could not retrieve its configured image.",
			EvidenceRefs: ids,
		},
	)

	detailKinds := map[string]bool{}
	truncated := false
	for _, observation := range evidence.Observations {
		if !containsString(ids, observation.ID) {
			continue
		}
		truncated = truncated || observation.Truncated
		for _, kind := range imagePullDetailKinds(eventMessage(observation)+" "+observation.Summary, eventReason(observation)) {
			detailKinds[kind] = true
		}
	}
	for _, ref := range evidence.EvidenceRefs {
		if !containsString(ids, ref.ID) {
			continue
		}
		for _, kind := range imagePullDetailKinds(ref.Summary, ref.Reason) {
			detailKinds[kind] = true
		}
		truncated = truncated || ref.Truncated
	}

	if truncated {
		result.Status = StatusInconclusive
		result.Warnings = append(result.Warnings, "image-pull evidence was truncated; specific failure verification is incomplete")
		result.MissingRequirements = append(result.MissingRequirements, d.Requirements(evidence)[1])
	}
	if len(detailKinds) == 0 {
		result.MissingRequirements = append(result.MissingRequirements, d.Requirements(evidence)[1])
		result.Warnings = append(result.Warnings, "the image-pull event does not contain a specific failure detail")
	} else if !truncated {
		for _, kind := range sortedKeys(detailKinds) {
			result.Claims = append(result.Claims, CandidateClaim{
				ID:           "image-pull-cause-" + kind,
				Type:         ClaimRootCause,
				Statement:    imagePullCauseStatement(kind),
				EvidenceRefs: ids,
			})
		}
	}

	if !truncated {
		result.Status = StatusSupported
	}
	result.Summary = fmt.Sprintf("Image-pull failure evidence matched %d bounded observation(s); causal verification remains separate.", len(ids))
	return result
}

func imagePullEvidenceIDs(evidence EvidenceSet) []string {
	ids := make(map[string]struct{})
	for _, observation := range evidence.Observations {
		if observation.Type != domain.ObservationTypeEvent || !isImagePullEvidence(eventReason(observation), eventMessage(observation)+" "+observation.Summary) {
			continue
		}
		if observation.ID != "" {
			ids[observation.ID] = struct{}{}
		}
	}
	for _, ref := range evidence.EvidenceRefs {
		if strings.EqualFold(strings.TrimSpace(ref.Kind), "event") && isImagePullEvidence(ref.Reason, ref.Summary) && ref.ID != "" {
			ids[ref.ID] = struct{}{}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func eventReason(observation domain.Observation) string {
	if observation.Value.Event != nil && observation.Value.Event.Reason != "" {
		return observation.Value.Event.Reason
	}
	return ""
}

func eventMessage(observation domain.Observation) string {
	if observation.Value.Event != nil {
		return observation.Value.Event.Message
	}
	return ""
}

func isImagePullReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "errimagepull", "imagepullbackoff":
		return true
	default:
		return false
	}
}

func isImagePullEvidence(reason, summary string) bool {
	if isImagePullReason(reason) {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(reason + " " + summary))
	return containsAny(text,
		"errimagepull",
		"imagepullbackoff",
		"failed to pull image",
		"pull access denied",
		"failed to resolve reference",
		"image pull failed",
	)
}

func imagePullDetailKinds(summary, reason string) []string {
	text := strings.ToLower(strings.TrimSpace(summary + " " + reason))
	result := make([]string, 0, 1)
	switch {
	case containsAny(text, "manifest unknown", "image not found", "not found", "repository does not exist"):
		result = append(result, "manifest-missing")
	case containsAny(text, "unauthorized", "authentication required", "pull access denied", "denied"):
		result = append(result, "registry-auth")
	case containsAny(text, "no such host", "name resolution", "dns", "timeout", "connection refused", "network is unreachable"):
		result = append(result, "registry-network")
	}
	return result
}

func imagePullCauseStatement(kind string) string {
	switch kind {
	case "manifest-missing":
		return "The registry reported that the requested image manifest could not be found."
	case "registry-auth":
		return "The registry rejected access to the requested image."
	case "registry-network":
		return "Image retrieval encountered a registry or name-resolution failure."
	default:
		return "The image-pull failure has a specific provider detail."
	}
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
