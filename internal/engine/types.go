package engine

import (
	"context"
	"time"
)

type Model struct {
	ID                     string
	Name                   string
	Family                 string
	ContextOptions         []ContextOption
	ReasoningEfforts       []string
	DefaultContextTier     string
	DefaultReasoningEffort string
}

type ContextOption struct {
	Tier   string
	Tokens int64
}

type ModelSelection struct {
	ModelID         string
	ContextTier     string
	ReasoningEffort string
}

type Session struct {
	ID              string
	Title           string
	Project         string
	Model           string
	ContextTier     string
	ReasoningEffort string
	SkillDigests    []string
	UpdatedAt       time.Time
}

type Message struct {
	Text     string
	Planning bool
}

type CompactResult struct {
	Success           bool
	MessagesRemoved   int64
	TokensRemoved     int64
	CurrentTokens     int64
	TokenLimit        int64
	MessagesRemaining int64
}

type ContextCategory struct {
	Tokens int64
}

type ContextEntry struct {
	ID         string
	Kind       string
	Label      string
	ParentID   string
	Tokens     int64
	Attributes map[string]string
}

type ContextMessage struct {
	ID     string
	Label  string
	Role   string
	Tokens int64
}

type ContextUsage struct {
	Model               string
	ModelSource         string
	TotalTokens         int64
	PromptTokenLimit    int64
	Limit               int64
	BufferTokens        int64
	CompactionThreshold int64
	SystemPrompt        ContextCategory
	CustomInstructions  ContextCategory
	SystemTools         ContextCategory
	MCPTools            ContextCategory
	Messages            ContextCategory
	FreeSpace           ContextCategory
	Buffer              ContextCategory
	Compactions         int64
	Entries             []ContextEntry
	HeaviestMessages    []ContextMessage
	Warnings            []string
}

type EventKind string

const (
	EventDelta      EventKind = "delta"
	EventMessage    EventKind = "message"
	EventToolStart  EventKind = "tool_start"
	EventToolOutput EventKind = "tool_output"
	EventToolEnd    EventKind = "tool_end"
	EventPermission EventKind = "permission"
	EventQuestion   EventKind = "question"
	EventUsage      EventKind = "usage"
	EventIdle       EventKind = "idle"
	EventError      EventKind = "error"
)

type Permission struct {
	ID          string
	Kind        string
	Path        string
	Command     string
	URL         string
	Description string
	Respond     func(allow bool) error
}

type Question struct {
	ID            string
	Prompt        string
	Choices       []string
	AllowFreeform bool
	Respond       func(answer string) error
	Cancel        func() error
}

type AccessReason string

const (
	AccessUnknown        AccessReason = "unknown"
	AccessAuthentication AccessReason = "authentication"
	AccessAuthorization  AccessReason = "authorization"
	AccessPolicy         AccessReason = "policy"
	AccessQuota          AccessReason = "quota"
	AccessBilling        AccessReason = "billing"
	AccessRateLimit      AccessReason = "rate_limit"
	AccessNetwork        AccessReason = "network"
)

type AccessIssue struct {
	Reason      AccessReason
	Code        string
	Remediation string
	StatusCode  int
}

type Event struct {
	Kind       EventKind
	ID         string
	SessionID  string
	MessageID  string
	ToolID     string
	Name       string
	Text       string
	Arguments  string
	Failed     bool
	History    bool
	Role       string
	Permission *Permission
	Question   *Question
	Access     *AccessIssue
	Err        error
}

type MCPServer struct {
	Name           string
	Command        string
	Args           []string
	Env            map[string]string
	Tools          []string
	TimeoutSeconds int
}

type Skill struct {
	Name      string
	Digest    string
	Directory string
}

type Engine interface {
	Start(context.Context) error
	Models(context.Context) ([]Model, error)
	NewSession(context.Context, ModelSelection) (Session, error)
	ResumeSession(context.Context, string) (Session, error)
	Sessions(context.Context) ([]Session, error)
	Send(context.Context, Message) error
	Context(context.Context) (ContextUsage, error)
	Compact(context.Context, string) (CompactResult, error)
	SetModel(context.Context, ModelSelection) error
	Abort(context.Context) error
	Events() <-chan Event
	Close() error
}

type Config struct {
	Project            string
	Home               string
	AccountID          string
	TokenSource        func(context.Context) (string, error)
	MCPServers         []MCPServer
	Skills             []Skill
	ActiveSkillDigests []string
}
