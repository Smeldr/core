package smeldr

import (
	"encoding/json"
	"time"
)

// Fields per type (D114 point 2): a record carries only the fields that apply
// to its type. The orchestration and authority types below have state flows
// with no published or scheduled state, so [Node.PublishedAt] and
// [Node.ScheduledAt] can never be set on them, and their JSON leaves both out.
// Run has no flow, so the fields can be set on it, and it keeps them, as every
// web content type does. Decoding is unchanged: a client that sends the two
// keys still decodes.

// omitPublishFields shadows Node's PublishedAt and ScheduledAt one level
// shallower, so encoding/json picks these nil fields and omits them.
type omitPublishFields struct {
	PublishedAt *time.Time `json:"PublishedAt,omitempty"`
	ScheduledAt *time.Time `json:"ScheduledAt,omitempty"`
}

// MarshalJSON encodes the Goal without PublishedAt and ScheduledAt, which a
// Goal's flow can never set (D114).
func (g Goal) MarshalJSON() ([]byte, error) {
	type plain Goal
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(g)})
}

// MarshalJSON encodes the Signal without PublishedAt and ScheduledAt, which a
// Signal's flow can never set (D114).
func (s Signal) MarshalJSON() ([]byte, error) {
	type plain Signal
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(s)})
}

// MarshalJSON encodes the Task without PublishedAt and ScheduledAt, which a
// Task's flow can never set (D114).
func (t Task) MarshalJSON() ([]byte, error) {
	type plain Task
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(t)})
}

// MarshalJSON encodes the Decision without PublishedAt and ScheduledAt, which
// a Decision's flow can never set (D114).
func (d Decision) MarshalJSON() ([]byte, error) {
	type plain Decision
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(d)})
}

// MarshalJSON encodes the Amendment without PublishedAt and ScheduledAt, which
// an Amendment's flow can never set (D114), and without the retired pilot
// (D112).
func (a Amendment) MarshalJSON() ([]byte, error) {
	type plain Amendment
	return json.Marshal(struct {
		plain
		omitPublishFields
		Pilot *string `json:"pilot,omitempty"`
	}{plain: plain(a)})
}

// MarshalJSON encodes the Rule without PublishedAt and ScheduledAt, which a
// Rule's flow can never set (D114).
func (r Rule) MarshalJSON() ([]byte, error) {
	type plain Rule
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(r)})
}

// MarshalJSON encodes the AuthorityStub without PublishedAt and ScheduledAt,
// which an AuthorityStub's flow can never set (D114).
func (s AuthorityStub) MarshalJSON() ([]byte, error) {
	type plain AuthorityStub
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(s)})
}

// MarshalJSON encodes the Organization without PublishedAt and ScheduledAt, which a
// place's flow can never set (D114, D142).
func (r OrganizationRecord) MarshalJSON() ([]byte, error) {
	type plain OrganizationRecord
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(r)})
}

// MarshalJSON encodes the Domain without PublishedAt and ScheduledAt, which a
// place's flow can never set (D114, D142).
func (d Domain) MarshalJSON() ([]byte, error) {
	type plain Domain
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(d)})
}

// MarshalJSON encodes the Area without PublishedAt and ScheduledAt, which a
// place's flow can never set (D114, D142).
func (a Area) MarshalJSON() ([]byte, error) {
	type plain Area
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(a)})
}

// MarshalJSON encodes the Set without PublishedAt and ScheduledAt, which a
// place's flow can never set (D114, D142).
func (s Set) MarshalJSON() ([]byte, error) {
	type plain Set
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(s)})
}

// MarshalJSON encodes the CoverageRule without PublishedAt and ScheduledAt,
// which its flow can never set (D114, T886).
func (r CoverageRule) MarshalJSON() ([]byte, error) {
	type plain CoverageRule
	return json.Marshal(struct {
		plain
		omitPublishFields
	}{plain: plain(r)})
}
