package workroom

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	KindKindDef        Kind = "kind-def"
	KindFoldActivation Kind = "fold-activation"
)

type FieldOperator string

const (
	FieldPresent FieldOperator = "present"
	FieldType    FieldOperator = "type"
	FieldMatches FieldOperator = "matches"
	FieldOneOf   FieldOperator = "one-of"
)

type ValueType string

const (
	ValueString     ValueType = "string"
	ValueEventID    ValueType = "event-id"
	ValueActorRef   ValueType = "actor-ref"
	ValuePathCommit ValueType = "path-commit"
)

type FieldConstraint struct {
	Operator FieldOperator `json:"op"`
	Name     string        `json:"name"`
	Type     ValueType     `json:"type,omitempty"`
	Pattern  string        `json:"pattern,omitempty"`
	Values   []string      `json:"values,omitempty"`
}

type BasisConstraint struct {
	Kinds []Kind `json:"kinds"`
	Min   int    `json:"min"`
	Max   int    `json:"max"`
}

type RenderClass string

const (
	RenderNote       RenderClass = "note"
	RenderProposal   RenderClass = "proposal"
	RenderCommitment RenderClass = "commitment"
	RenderResult     RenderClass = "result"
	RenderDissent    RenderClass = "dissent"
	RenderArtifact   RenderClass = "artifact"
	RenderGovernance RenderClass = "governance"
)

type StalenessMode string

const (
	StalenessPropagates StalenessMode = "propagates"
	StalenessTerminal   StalenessMode = "terminal"
	StalenessExempt     StalenessMode = "exempt"
)

type Lifecycle string

const (
	LifecycleNone    Lifecycle = "none"
	LifecycleRequest Lifecycle = "request"
	LifecyclePromise Lifecycle = "promise"
	LifecycleReport  Lifecycle = "report"
)

const (
	SatisfierNone                 = "none"
	SatisfierOriginatingRequester = "originating-requester"
)

// KindDefinition is the finite, data-only language interpreted by the fold.
// Source is projection metadata: "starter" for the compatibility catalog or
// the event ID of the ratified kind-def that supplied the active version.
type KindDefinition struct {
	Name       Kind              `json:"name"`
	Fields     []FieldConstraint `json:"fields"`
	Basis      []BasisConstraint `json:"basis"`
	Satisfier  string            `json:"satisfier"`
	Render     RenderClass       `json:"render"`
	Staleness  StalenessMode     `json:"staleness"`
	Lifecycle  Lifecycle         `json:"lifecycle"`
	Guidance   string            `json:"guidance"`
	Source     string            `json:"source"`
	RatifiedBy string            `json:"ratified_by,omitempty"`
}

type FoldTransition struct {
	Activation   string `json:"activation"`
	Ratification string `json:"ratification"`
	Fold         string `json:"fold"`
	Entry        string `json:"entry"`
	Interface    string `json:"interface"`
	Toolchain    string `json:"toolchain"`
	Prefix       bool   `json:"prefix"`
}

type FoldBinding struct {
	Status      string           `json:"status"`
	Reason      string           `json:"reason,omitempty"`
	Transitions []FoldTransition `json:"transitions"`
}

type Vocabulary struct {
	Definitions []KindDefinition `json:"definitions"`
	Binding     FoldBinding      `json:"binding"`
}

// StarterLifecycle identifies compatibility-catalog kinds without constructing
// a projection. Callers still need the active vocabulary for declared kinds.
func StarterLifecycle(kind Kind) (Lifecycle, bool) {
	definition, ok := starterCatalog()[kind]
	return definition.Lifecycle, ok
}

// UndefinedKindWarning is what an author has to be told when the kind they
// wrote is one this record does not define. The act is still admitted and
// still visible as an attempt — that is deliberate, and the fold still
// projects it as undefined-kind — but no rule reads it, so a promise or a
// report written under such a kind never becomes one. Saying nothing is how an
// author comes to believe a commitment was made that nobody can see.
//
// The empty string means the kind is defined and there is nothing to say. The
// list of kinds comes from the live definitions rather than any compiled-in
// set, so a workroom that has declared its own kinds, or bound its own fold,
// names the kinds it actually has.
func (v Vocabulary) UndefinedKindWarning(kind Kind) string {
	names := make([]string, 0, len(v.Definitions))
	for _, definition := range v.Definitions {
		if definition.Name == kind {
			return ""
		}
		names = append(names, string(definition.Name))
	}
	defined := "no kinds are defined here"
	if len(names) != 0 {
		defined = "kinds defined here: " + strings.Join(names, ", ")
	}
	return fmt.Sprintf("kind %q is not defined in this workroom. The act was recorded and stays visible, "+
		"but no rule reads it: it projects as undefined-kind, and any promise, report, or other lifecycle "+
		"edge it was meant to carry does not form. %s", kind, defined)
}

func starterCatalog() map[Kind]KindDefinition {
	definitions := []KindDefinition{
		starter(KindAssert, nil, nil, "role:ratifier", RenderNote, LifecycleNone,
			"Record a durable claim. Ratification marks a claim as adopted; cite the acts that ground it."),
		starter(KindPropose, nil, nil, "role:ratifier", RenderProposal, LifecycleNone,
			"Offer a decision for ratification. State the choice and the consequences it adopts."),
		starter(KindRequest, []FieldConstraint{
			{Operator: FieldPresent, Name: "to"}, {Operator: FieldType, Name: "to", Type: ValueActorRef},
			{Operator: FieldPresent, Name: "conditions"},
		}, nil, SatisfierNone, RenderCommitment, LifecycleRequest,
			"Ask a named participant for an outcome and state observable conditions of satisfaction."),
		starter(KindPromise, nil, countKinds(1, 1, KindRequest), SatisfierNone, RenderCommitment, LifecyclePromise,
			"Accept exactly one request. Rest on that request and promise only work you can own."),
		starter(KindReport, nil, countKinds(1, 2, KindPromise, KindRequest), SatisfierOriginatingRequester, RenderResult, LifecycleReport,
			"Report the result of exactly one promise, or of the request you were asked directly, with the tests and conditions actually met."),
		starter(KindDissent, nil, nil, SatisfierNone, RenderDissent, LifecycleNone,
			"Preserve a concrete disagreement against the act it concerns; dissent does not rewrite history."),
		starter(KindArtifact, present("path", "commit"), nil, SatisfierNone, RenderArtifact, LifecycleNone,
			"Point to an exact repository path and commit that a reader can inspect."),
		starter(KindRoster, present("actor", "name", "role"), nil, "role:ratifier", RenderGovernance, LifecycleNone,
			"Govern durable membership and authority. Participant grants also carry the actor kind."),
		starter(KindInfraKey, present("service", "public_key"), nil, "role:ratifier", RenderGovernance, LifecycleNone,
			"Bind an infrastructure service name to its public key."),
		starter(KindSeal, nil, nil, "role:ratifier", RenderGovernance, LifecycleNone,
			"Record a governed seal over the workroom state."),
		starter(KindAdmissionProfile, present("bundle", "contract", "genesis"), nil, "role:ratifier", RenderGovernance, LifecycleNone,
			"Activate an admission profile. The newest ratified profile matching this genesis governs; retiring one restores its predecessor."),
		starter(KindKindDef, present("name", "fields", "basis", "satisfier", "render", "staleness", "lifecycle", "guidance"), nil, "role:ratifier", RenderGovernance, LifecycleNone,
			"Declare a kind using the finite constraint algebra. Guidance is visible to actors and powerless in the fold."),
	}
	catalog := make(map[Kind]KindDefinition, len(definitions))
	for _, definition := range definitions {
		catalog[definition.Name] = definition
	}
	return catalog
}

// legacyFoldActivationDefinition is the read bridge for append-only state@0
// history. New vocabulary does not contain this kind: fold upgrades are host
// bindings, authorized by the initializing key before an application folds.
func legacyFoldActivationDefinition() KindDefinition {
	return starter(KindFoldActivation, []FieldConstraint{
		{Operator: FieldPresent, Name: "fold"}, {Operator: FieldType, Name: "fold", Type: ValuePathCommit},
		{Operator: FieldPresent, Name: "entry"}, {Operator: FieldPresent, Name: "interface"},
		{Operator: FieldPresent, Name: "toolchain"}, {Operator: FieldPresent, Name: "prefix"},
		{Operator: FieldOneOf, Name: "prefix", Values: []string{"genesis"}},
	}, nil, "role:ratifier", RenderGovernance, LifecycleNone,
		"Historical state@0 fold activation retained for deterministic replay; new upgrades use the host binding.")
}

func starter(name Kind, fields []FieldConstraint, basis []BasisConstraint, satisfier string, render RenderClass, lifecycle Lifecycle, guidance string) KindDefinition {
	if fields == nil {
		fields = []FieldConstraint{}
	}
	if basis == nil {
		basis = []BasisConstraint{}
	}
	return KindDefinition{
		Name: name, Fields: fields, Basis: basis, Satisfier: satisfier,
		Render: render, Staleness: StalenessPropagates, Lifecycle: lifecycle,
		Guidance: guidance, Source: "starter",
	}
}

func present(names ...string) []FieldConstraint {
	constraints := make([]FieldConstraint, 0, len(names))
	for _, name := range names {
		constraints = append(constraints, FieldConstraint{Operator: FieldPresent, Name: name})
	}
	return constraints
}

func countKinds(minimum, maximum int, kinds ...Kind) []BasisConstraint {
	return []BasisConstraint{{Kinds: append([]Kind(nil), kinds...), Min: minimum, Max: maximum}}
}

func decodeKindDefinition(state State, source string) (KindDefinition, error) {
	if _, carriesCode := state.Body["fold"]; carriesCode {
		return KindDefinition{}, errors.New("fold is not part of the declarative kind-definition language")
	}
	definition := KindDefinition{
		Name: Kind(state.Body["name"]), Satisfier: state.Body["satisfier"],
		Render: RenderClass(state.Body["render"]), Staleness: StalenessMode(state.Body["staleness"]),
		Lifecycle: Lifecycle(state.Body["lifecycle"]), Guidance: state.Body["guidance"], Source: source,
	}
	if err := decodeCanonicalString(state.Body["fields"], &definition.Fields); err != nil {
		return KindDefinition{}, fmt.Errorf("fields: %w", err)
	}
	if definition.Fields == nil {
		return KindDefinition{}, errors.New("fields must be a JSON array")
	}
	if err := decodeCanonicalString(state.Body["basis"], &definition.Basis); err != nil {
		return KindDefinition{}, fmt.Errorf("basis: %w", err)
	}
	if definition.Basis == nil {
		return KindDefinition{}, errors.New("basis must be a JSON array")
	}
	if err := validateDefinition(definition); err != nil {
		return KindDefinition{}, err
	}
	return definition, nil
}

func decodeCanonicalString(value string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	canonical, err := json.Marshal(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, []byte(value)) {
		return errors.New("value is not canonical JSON")
	}
	return nil
}

// identifier is the enumerated token grammar for every name a definition can
// introduce: a kind name, a role name, a required body field name. Lowercase
// ASCII words joined by single hyphens or underscores — the shape every name in
// the starter catalog already has. Enumerating it keeps a name that no actor
// could ever satisfy, such as one carrying a space, out of the catalog instead
// of leaving it to fail silently at every later position.
var identifier = regexp.MustCompile(`^[a-z][a-z0-9]*([-_][a-z0-9]+)*$`)

// foldInterpreted names the kinds the fold reads directly rather than through
// their definition. fold-activation remains reserved because state@0 history
// still passes through its migration bridge; allowing a declaration to reuse
// that name would make one token mean both declarative data and trusted code.
var foldInterpreted = map[Kind]bool{KindKindDef: true, KindFoldActivation: true, KindRoster: true}

func validateDefinition(definition KindDefinition) error {
	if definition.Guidance == "" {
		return errors.New("guidance is required")
	}
	if foldInterpreted[definition.Name] {
		return fmt.Errorf("kind %q is interpreted by the fold and cannot be redefined", definition.Name)
	}
	if !identifier.MatchString(string(definition.Name)) {
		return fmt.Errorf("kind name %q is not an identifier", definition.Name)
	}
	for _, field := range definition.Fields {
		if !identifier.MatchString(field.Name) {
			return fmt.Errorf("field constraint name %q is not an identifier", field.Name)
		}
		switch field.Operator {
		case FieldPresent:
			if field.Type != "" || field.Pattern != "" || len(field.Values) != 0 {
				return fmt.Errorf("present(%s) carries operands", field.Name)
			}
		case FieldType:
			switch field.Type {
			case ValueString, ValueEventID, ValueActorRef, ValuePathCommit:
			default:
				return fmt.Errorf("unsupported type %q", field.Type)
			}
			if field.Pattern != "" || len(field.Values) != 0 {
				return fmt.Errorf("type(%s) carries operands", field.Name)
			}
		case FieldMatches:
			if field.Pattern == "" {
				return fmt.Errorf("matches(%s) requires a pattern", field.Name)
			}
			if _, err := regexp.Compile(field.Pattern); err != nil {
				return fmt.Errorf("matches(%s): %w", field.Name, err)
			}
			if field.Type != "" || len(field.Values) != 0 {
				return fmt.Errorf("matches(%s) carries operands", field.Name)
			}
		case FieldOneOf:
			if len(field.Values) == 0 {
				return fmt.Errorf("one-of(%s) requires values", field.Name)
			}
			if field.Type != "" || field.Pattern != "" {
				return fmt.Errorf("one-of(%s) carries operands", field.Name)
			}
		default:
			return fmt.Errorf("unsupported field operator %q", field.Operator)
		}
	}
	for _, basis := range definition.Basis {
		if len(basis.Kinds) == 0 || basis.Min < 0 || basis.Max < basis.Min {
			return errors.New("basis count requires kinds and a finite min..max range")
		}
		seen := make(map[Kind]bool)
		for _, kind := range basis.Kinds {
			if seen[kind] {
				return errors.New("basis kinds must be unique")
			}
			if !identifier.MatchString(string(kind)) {
				return fmt.Errorf("basis kind %q is not an identifier", kind)
			}
			seen[kind] = true
		}
	}
	if definition.Satisfier != SatisfierNone && definition.Satisfier != SatisfierOriginatingRequester && !strings.HasPrefix(definition.Satisfier, "role:") {
		return fmt.Errorf("unsupported satisfier %q", definition.Satisfier)
	}
	if role, held := strings.CutPrefix(definition.Satisfier, "role:"); held && !identifier.MatchString(role) {
		return fmt.Errorf("role name %q is not an identifier", role)
	}
	switch definition.Render {
	case RenderNote, RenderProposal, RenderCommitment, RenderResult, RenderDissent, RenderArtifact, RenderGovernance:
	default:
		return fmt.Errorf("unsupported render class %q", definition.Render)
	}
	switch definition.Staleness {
	case StalenessPropagates, StalenessTerminal, StalenessExempt:
	default:
		return fmt.Errorf("unsupported staleness mode %q", definition.Staleness)
	}
	switch definition.Lifecycle {
	case LifecycleNone, LifecycleRequest, LifecyclePromise, LifecycleReport:
	default:
		return fmt.Errorf("unsupported lifecycle %q", definition.Lifecycle)
	}
	return nil
}

func validateFields(definition KindDefinition, body map[string]string) error {
	for _, constraint := range definition.Fields {
		value, held := body[constraint.Name]
		switch constraint.Operator {
		case FieldPresent:
			if !held || value == "" {
				return fmt.Errorf("%s state requires body.%s", definition.Name, constraint.Name)
			}
		case FieldType:
			if !held {
				continue
			}
			if !matchesType(constraint.Type, value) {
				return fmt.Errorf("%s body.%s is not %s", definition.Name, constraint.Name, constraint.Type)
			}
		case FieldMatches:
			if held && !regexp.MustCompile(constraint.Pattern).MatchString(value) {
				return fmt.Errorf("%s body.%s does not match its declared pattern", definition.Name, constraint.Name)
			}
		case FieldOneOf:
			if held && !contains(constraint.Values, value) {
				return fmt.Errorf("%s body.%s is not one of its declared values", definition.Name, constraint.Name)
			}
		}
	}
	return nil
}

func matchesType(valueType ValueType, value string) bool {
	switch valueType {
	case ValueString:
		return value != ""
	case ValueEventID, ValueActorRef:
		return value != "" && !strings.ContainsAny(value, "\r\n\t ")
	case ValuePathCommit:
		at := strings.LastIndexByte(value, '@')
		return at > 0 && at < len(value)-1
	default:
		return false
	}
}

func sortedDefinitions(catalog map[Kind]KindDefinition) []KindDefinition {
	definitions := make([]KindDefinition, 0, len(catalog))
	for _, definition := range catalog {
		definition.Fields = append([]FieldConstraint(nil), definition.Fields...)
		definition.Basis = append([]BasisConstraint(nil), definition.Basis...)
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	return definitions
}
