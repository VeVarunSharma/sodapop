package engine

import (
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestModelsAreDiscoveredAndDisabledModelsExcluded(t *testing.T) {
	client := newFakeClient()
	client.models = append(client.models, copilot.ModelInfo{ID: "disabled", Policy: &copilot.ModelPolicy{State: "disabled"}})
	engine := testEngine(t, testConfig(t), client, nil)
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	models, err := engine.Models(t.Context())
	if err != nil || len(models) != 2 || models[0].ID != "model-a" || models[1].ID != "model-b" {
		t.Fatalf("wrong discovery result: %+v, %v", models, err)
	}
	if _, err := engine.NewSession(t.Context(), ModelSelection{ModelID: "disabled", ContextTier: "default"}); err == nil {
		t.Fatal("enabled an enterprise-disabled model")
	}
	client.mu.Lock()
	client.models = nil
	client.mu.Unlock()
	if _, err := engine.Models(t.Context()); err == nil {
		t.Fatal("substituted a hardcoded model when discovery was empty")
	}
}

func TestModelCatalogMapsFamiliesContextSizesAndEffortOrder(t *testing.T) {
	defaultTokens, longTokens := int64(128000), int64(1000000)
	client := newFakeClient()
	client.models = []copilot.ModelInfo{{
		ID: "gpt-5.6-luna", Name: "GPT-5.6 Luna",
		Capabilities: copilot.ModelCapabilities{
			Supports: copilot.ModelSupports{ReasoningEffort: true},
		},
		Billing: &copilot.ModelBilling{TokenPrices: &rpc.ModelBillingTokenPrices{
			MaxPromptTokens: &defaultTokens,
			LongContext: &rpc.ModelBillingTokenPricesLongContext{
				MaxPromptTokens: &longTokens,
			},
		}},
		SupportedReasoningEfforts: []string{"high", "low", "xhigh", "medium"},
		DefaultReasoningEffort:    "medium",
	}}
	engine := testEngine(t, testConfig(t), client, nil)
	if err := engine.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	models, err := engine.Models(t.Context())
	if err != nil || len(models) != 1 {
		t.Fatalf("models: %+v, %v", models, err)
	}
	model := models[0]
	if model.Family != "OpenAI" || model.DefaultContextTier != "default" ||
		len(model.ContextOptions) != 2 || model.ContextOptions[0].Tokens != defaultTokens ||
		model.ContextOptions[1].Tier != "long_context" || model.ContextOptions[1].Tokens != longTokens ||
		strings.Join(model.ReasoningEfforts, ",") != "low,medium,high,xhigh" {
		t.Fatalf("catalog metadata was not normalized: %+v", model)
	}
	if got := modelFamily("Future Model", "provider/future"); got != "Other" {
		t.Fatalf("unknown model family = %q", got)
	}
}

func TestEffectiveToolsAreValidatedNotAssumed(t *testing.T) {
	valid := []rpc.CurrentToolMetadata{{Name: "view"}, {Name: "rg"}, {Name: "edit"}, {Name: "bash"}, {Name: "ask_user"}}
	if err := validateTools(valid); err != nil {
		t.Fatal(err)
	}
	for _, tools := range [][]rpc.CurrentToolMetadata{
		nil,
		{{Name: "view"}, {Name: "edit"}, {Name: "ask_user"}},
		append(append([]rpc.CurrentToolMetadata{}, valid...), rpc.CurrentToolMetadata{Name: "skill"}),
		append(append([]rpc.CurrentToolMetadata{}, valid...), rpc.CurrentToolMetadata{Name: "web_fetch"}),
		{{Name: "view", MCPServerName: copilot.String("unexpected")}, {Name: "edit"}, {Name: "bash"}, {Name: "ask_user"}},
	} {
		if err := validateTools(tools); err == nil {
			t.Fatalf("accepted missing or implicitly imported tools: %+v", tools)
		}
	}
}
