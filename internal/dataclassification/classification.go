package dataclassification

import (
	"fmt"
	"sort"
	"strings"

	"fluxseer/api/v1alpha1"
	"fluxseer/internal/domain"
)

const (
	DecisionAllowed  = "Allowed"
	DecisionRejected = "Rejected"

	ReasonAllowed                  = "Allowed"
	ReasonClassificationExceeded   = "ClassificationExceeded"
	ReasonSensitivityTagDenied     = "SensitivityTagDenied"
	ReasonRedactionRequired        = "RedactionRequired"
	ReasonExternalTransmissionDeny = "ExternalTransmissionDisabled"
)

var levelRank = map[string]int{
	strings.ToLower(v1alpha1.DataClassificationLevelPublic):       0,
	strings.ToLower(v1alpha1.DataClassificationLevelInternal):     1,
	strings.ToLower(v1alpha1.DataClassificationLevelConfidential): 2,
	strings.ToLower(v1alpha1.DataClassificationLevelRestricted):   3,
}

type Decision struct {
	Decision              string
	Reason                string
	Message               string
	MaximumObserved       string
	MaximumAllowed        string
	MaximumSent           string
	SensitivityTagsSent   []string
	ClassificationVersion string
}

func DefaultForObservation(queryType domain.QueryType, text string) v1alpha1.DataClassification {
	classification := v1alpha1.DataClassification{
		Level:           v1alpha1.DataClassificationLevelInternal,
		SensitivityTags: []string{v1alpha1.SensitivityTagInfrastructureMetadata},
		Source:          v1alpha1.DataClassificationSourceDefault,
		PolicyVersion:   v1alpha1.DataClassificationPolicyVersion,
	}
	if queryType == domain.QueryTypeLog {
		classification.Level = v1alpha1.DataClassificationLevelConfidential
	}
	if LooksCredentialLike(text) {
		classification = Merge(classification, v1alpha1.DataClassification{
			Level:           v1alpha1.DataClassificationLevelRestricted,
			SensitivityTags: []string{v1alpha1.SensitivityTagCredentialLike},
			Source:          v1alpha1.DataClassificationSourceContentDetection,
			PolicyVersion:   v1alpha1.DataClassificationPolicyVersion,
		})
		classification.Source = v1alpha1.DataClassificationSourceContentDetection
	}
	return classification
}

func Merge(items ...v1alpha1.DataClassification) v1alpha1.DataClassification {
	out := v1alpha1.DataClassification{
		Level:         v1alpha1.DataClassificationLevelInternal,
		Source:        v1alpha1.DataClassificationSourceDefault,
		PolicyVersion: v1alpha1.DataClassificationPolicyVersion,
	}
	tags := map[string]struct{}{}
	for _, item := range items {
		if strings.TrimSpace(item.Level) != "" && CompareLevels(item.Level, out.Level) > 0 {
			out.Level = NormalizeLevel(item.Level)
		}
		if strings.TrimSpace(item.Source) != "" && item.Source != v1alpha1.DataClassificationSourceDefault {
			out.Source = item.Source
		}
		if strings.TrimSpace(item.PolicyVersion) != "" {
			out.PolicyVersion = item.PolicyVersion
		}
		for _, tag := range item.SensitivityTags {
			normalized := NormalizeSensitivityTag(tag)
			if normalized == "" {
				continue
			}
			tags[normalized] = struct{}{}
		}
	}
	out.SensitivityTags = sortedKeys(tags)
	return out
}

func BundleClassification(refs []v1alpha1.EvidenceRef) v1alpha1.DataClassification {
	if len(refs) == 0 {
		return v1alpha1.DataClassification{
			Level:         v1alpha1.DataClassificationLevelInternal,
			Source:        v1alpha1.DataClassificationSourceDefault,
			PolicyVersion: v1alpha1.DataClassificationPolicyVersion,
		}
	}
	items := make([]v1alpha1.DataClassification, 0, len(refs))
	for _, ref := range refs {
		if ref.Classification != nil {
			items = append(items, *ref.Classification)
			continue
		}
		items = append(items, DefaultForEvidenceKind(ref.Kind, ref.Summary))
	}
	classification := Merge(items...)
	classification.Source = v1alpha1.DataClassificationSourceInherited
	return classification
}

func DefaultForEvidenceKind(kind string, text string) v1alpha1.DataClassification {
	return DefaultForObservation(queryTypeForKind(kind), text)
}

func EvaluateProviderPolicy(policy v1alpha1.ModelProviderDataPolicy, refs []v1alpha1.EvidenceRef) Decision {
	observed := BundleClassification(refs)
	allowed := NormalizeLevel(policy.MaximumClassification)
	if allowed == "" {
		allowed = v1alpha1.DataClassificationLevelInternal
	}
	decision := Decision{
		Decision:              DecisionAllowed,
		Reason:                ReasonAllowed,
		MaximumObserved:       observed.Level,
		MaximumAllowed:        allowed,
		MaximumSent:           observed.Level,
		SensitivityTagsSent:   append([]string(nil), observed.SensitivityTags...),
		ClassificationVersion: v1alpha1.DataClassificationPolicyVersion,
	}
	if CompareLevels(observed.Level, allowed) > 0 {
		decision.Decision = DecisionRejected
		decision.Reason = ReasonClassificationExceeded
		decision.MaximumSent = ""
		decision.SensitivityTagsSent = nil
		decision.Message = fmt.Sprintf("evidence classification %s exceeds allowed maximum %s", observed.Level, allowed)
		return decision
	}
	denied := deniedTag(observed.SensitivityTags, policy.DeniedSensitivityTags)
	if denied != "" {
		decision.Decision = DecisionRejected
		decision.Reason = ReasonSensitivityTagDenied
		decision.MaximumSent = ""
		decision.SensitivityTagsSent = nil
		decision.Message = fmt.Sprintf("evidence sensitivity tag %s is denied by provider data policy", denied)
		return decision
	}
	if policy.RequireRedaction && !allRefsRedacted(refs) {
		decision.Decision = DecisionRejected
		decision.Reason = ReasonRedactionRequired
		decision.MaximumSent = ""
		decision.SensitivityTagsSent = nil
		decision.Message = "provider data policy requires redaction metadata on all transmitted evidence"
		return decision
	}
	return decision
}

func NormalizeLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "public":
		return v1alpha1.DataClassificationLevelPublic
	case "internal", "":
		return v1alpha1.DataClassificationLevelInternal
	case "confidential":
		return v1alpha1.DataClassificationLevelConfidential
	case "restricted":
		return v1alpha1.DataClassificationLevelRestricted
	default:
		return strings.TrimSpace(level)
	}
}

func CompareLevels(a string, b string) int {
	return levelValue(a) - levelValue(b)
}

func levelValue(level string) int {
	if value, ok := levelRank[strings.ToLower(NormalizeLevel(level))]; ok {
		return value
	}
	return levelRank[strings.ToLower(v1alpha1.DataClassificationLevelRestricted)]
}

func NormalizeSensitivityTag(tag string) string {
	switch strings.ToLower(strings.TrimSpace(tag)) {
	case "credentiallike", "secretlike":
		return v1alpha1.SensitivityTagCredentialLike
	case "personaldata":
		return v1alpha1.SensitivityTagPersonalData
	case "customerdata":
		return v1alpha1.SensitivityTagCustomerData
	case "sourcecode":
		return v1alpha1.SensitivityTagSourceCode
	case "infrastructuremetadata":
		return v1alpha1.SensitivityTagInfrastructureMetadata
	case "securitysensitive":
		return v1alpha1.SensitivityTagSecuritySensitive
	default:
		return strings.TrimSpace(tag)
	}
}

func LooksCredentialLike(text string) bool {
	normalized := strings.ToLower(text)
	return strings.Contains(normalized, "token=") ||
		strings.Contains(normalized, "password=") ||
		strings.Contains(normalized, "api_key") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "secret=") ||
		strings.Contains(normalized, "bearer ")
}

func queryTypeForKind(kind string) domain.QueryType {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.TrimSuffix(kind, "observation")
	switch kind {
	case "metric":
		return domain.QueryTypeMetric
	case "log":
		return domain.QueryTypeLog
	case "event", "kubernetesevent":
		return domain.QueryTypeEvent
	case "deploymentcondition":
		return domain.QueryTypeDeploymentCondition
	default:
		return domain.QueryTypeEvent
	}
}

func deniedTag(tags []string, denied []string) string {
	deniedSet := map[string]struct{}{}
	for _, tag := range denied {
		normalized := NormalizeSensitivityTag(tag)
		if normalized != "" {
			deniedSet[normalized] = struct{}{}
		}
	}
	for _, tag := range tags {
		normalized := NormalizeSensitivityTag(tag)
		if _, ok := deniedSet[normalized]; ok {
			return normalized
		}
	}
	return ""
}

func allRefsRedacted(refs []v1alpha1.EvidenceRef) bool {
	for _, ref := range refs {
		if strings.TrimSpace(ref.RedactionProfile) == "" {
			return false
		}
	}
	return true
}

func sortedKeys(in map[string]struct{}) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
