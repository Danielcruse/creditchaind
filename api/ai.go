package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/creditchain/creditchaind/core"
)

// This file is CreditChain's site-wide AI assistant - a chat surface
// backed by the Claude API that can answer questions and help with
// two concrete jobs the user asked for by name: helping a Trading Hub
// think through its trade rules, and helping a borrower/lender work
// out loan affordability. It is deliberately READ-ONLY: every tool the
// assistant can call only looks up or computes something, never
// signs, records, or moves anything. If the assistant thinks the user
// should take an action (post a rate, draw a loan, set a rule), it
// says so in its reply and the human clicks the real button
// themselves - the assistant never gets to act "with the user's
// authority" the way a signed request would. This mirrors the same
// action-authorization boundary this whole codebase already enforces
// everywhere else (see core/request_auth.go): only a real signature
// from the wallet itself authorizes a mutating action, and that stays
// true here too - the assistant is never handed a signing key.
//
// aiConfigured follows the exact same fail-closed convention as
// smtpConfigured/requireAdmin/networkSecret: bare os.Getenv, no
// startup log, no config struct. An operator opts in by setting
// ANTHROPIC_API_KEY; every call site checks this first.
func aiConfigured() bool {
	return os.Getenv("ANTHROPIC_API_KEY") != ""
}

const anthropicMessagesURL = "https://api.anthropic.com/v1/messages"
const anthropicAssistantModel = "claude-opus-4-8"

// --- Wire types for the Anthropic Messages API -----------------------------
//
// Raw net/http rather than the official SDK, to answer one chat turn
// without adding a new Go module dependency to this project (see
// go.mod's existing minimal-dependency footprint - the frontend has a
// zero-unaudited-JS-dependency convention; this is the backend
// equivalent for the one feature that would otherwise need one).

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// tool_use fields (present on blocks Claude sends us)
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result fields (present on blocks we send back)
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Error      *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// callAnthropic makes one non-streaming Messages API call. The
// assistant's tool loop (below) calls this in a small bounded loop,
// same "manual agentic loop" shape as every SDK's own reference
// pattern - just without the SDK.
func callAnthropic(reqBody anthropicRequest) (*anthropicResponse, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ai: ANTHROPIC_API_KEY not set")
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, anthropicMessagesURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ai: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("ai: could not parse response (status %d)", resp.StatusCode)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("ai: %s", parsed.Error.Message)
	}
	return &parsed, nil
}

// --- Tool definitions --------------------------------------------------

var assistantTools = []anthropicTool{
	{
		Name:        "get_my_wallet",
		Description: "Get the current user's own wallet balance, reputation score, credit tier, and voting power. Takes no input - always resolves to whichever wallet is asking.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	},
	{
		Name:        "list_open_loan_offers",
		Description: "List currently open peer-funded loan offers a borrower could draw against - symbol, interest rate, duration, and remaining capital for each. Call this when the user asks what loan offers are available or wants to compare lenders.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	},
	{
		Name:        "calculate_loan_repayment",
		Description: "Calculate exactly what a borrower would owe on a loan of a given principal amount and interest rate - CreditChain loans are flat-rate (not compounding, not annualized by duration): total repayment = principal + principal * ratePercent / 100. Call this whenever the user asks 'how much would I owe', 'can I afford this loan', or gives you a principal and rate to check.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"principalAmount": {"type": "number", "description": "The loan principal, in whole token units"},
				"ratePercent": {"type": "number", "description": "The flat interest rate as a percent, e.g. 8 for 8%"}
			},
			"required": ["principalAmount", "ratePercent"]
		}`),
	},
	{
		Name:        "get_hub_rules",
		Description: "Get a Trading Hub's currently posted trade rules (free-text terms, payment windows per trade direction, minimum reputation to apply as an agent). Call this when helping a hub review or improve its own posted rules, or when a trader asks what a specific hub's rules are.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"hubAddress": {"type": "string", "description": "The hub's wallet address. If asking about the current user's own hub, use their own address from get_my_wallet context."}
			},
			"required": ["hubAddress"]
		}`),
	},
}

// executeAssistantTool runs one tool call server-side and returns its
// result as plain text for the model to read. callerAddress is the
// verified address from the signed envelope this whole chat request
// arrived under - never client-supplied, so "my wallet" can't be
// spoofed to read someone else's.
func executeAssistantTool(name string, input json.RawMessage, callerAddress string) (string, bool) {
	switch name {
	case "get_my_wallet":
		wallet := walletStore.Get(callerAddress)
		profile := creditEngine.Profiles.Get(callerAddress)
		result := map[string]any{
			"address":     callerAddress,
			"cctBalance":  wallet.CCTBalance,
			"reputation":  wallet.Reputation,
			"creditTier":  core.TierForReputation(wallet.Reputation),
			"votingPower": wallet.VotingPower,
			"loansRepaid": profile.LoansRepaid,
			"loansFunded": profile.LoansFunded,
		}
		b, _ := json.Marshal(result)
		return string(b), false

	case "list_open_loan_offers":
		offers := loanOfferStore.List()
		type offerSummary struct {
			Symbol          string `json:"symbol"`
			RatePercent     uint64 `json:"ratePercent"`
			DurationSeconds uint64 `json:"durationSeconds"`
			RemainingAmount uint64 `json:"remainingAmount"`
		}
		summaries := make([]offerSummary, 0, len(offers))
		for _, o := range offers {
			if o.Status != core.LoanOfferOpen || o.RemainingAmount == 0 {
				continue
			}
			summaries = append(summaries, offerSummary{
				Symbol:          o.Symbol,
				RatePercent:     o.RatePercent,
				DurationSeconds: o.DurationSeconds,
				RemainingAmount: o.RemainingAmount,
			})
		}
		b, _ := json.Marshal(summaries)
		return string(b), false

	case "calculate_loan_repayment":
		var args struct {
			PrincipalAmount float64 `json:"principalAmount"`
			RatePercent     float64 `json:"ratePercent"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "invalid tool input: " + err.Error(), true
		}
		if args.PrincipalAmount <= 0 || args.RatePercent < 0 {
			return "principalAmount must be positive and ratePercent must be non-negative", true
		}
		interest := args.PrincipalAmount * args.RatePercent / 100
		total := args.PrincipalAmount + interest
		result := map[string]any{
			"principal":      args.PrincipalAmount,
			"ratePercent":    args.RatePercent,
			"interestOwed":   interest,
			"totalRepayment": total,
		}
		b, _ := json.Marshal(result)
		return string(b), false

	case "get_hub_rules":
		var args struct {
			HubAddress string `json:"hubAddress"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return "invalid tool input: " + err.Error(), true
		}
		if !companyStore.IsVerified(args.HubAddress) {
			return "that address is not a verified Trading Hub", true
		}
		rules, ok := hubRulesStore.Get(args.HubAddress)
		if !ok {
			return "this hub has not posted any rules yet", false
		}
		result := map[string]any{
			"text":                 rules.Text,
			"paymentWindowMinutes": rules.PaymentWindowMinutes,
			"minReputationToApply": rules.MinReputationToApply,
		}
		b, _ := json.Marshal(result)
		return string(b), false

	default:
		return "unknown tool: " + name, true
	}
}

const assistantSystemPrompt = `You are the CreditChain assistant, built into the CreditChain OUTERNET app. CreditChain is a testnet decentralized reputation and lending network - CCT/REPT/ETHTEST/USDCTEST are testnet tokens with no real monetary value.

You help users with two things in particular, in addition to general questions about how CreditChain works:
1. Loan affordability - when someone asks whether they can afford a loan, or gives you a principal and rate, use the calculate_loan_repayment tool to give them exact numbers rather than estimating.
2. Trading Hub rule-setting - when a Trading Hub operator asks for help writing or reviewing their trade rules, use get_hub_rules to see what they currently have (if anything), then give concrete, specific suggestions (payment window lengths, what to state clearly, common gaps).

Ground every factual claim about a specific wallet, loan offer, or hub in an actual tool call - never guess or make up numbers. If a tool isn't relevant, don't call it.

You cannot take any action on the user's behalf - you cannot post a rate, submit a trade, draw a loan, or sign anything. If the user wants to actually do something, tell them clearly what to click and where (e.g. "you can post this on your Trading Hub's Settings page"), don't imply you did it yourself.

Keep answers concise and concrete. This is a chat widget, not a report.`

// runAssistantLoop drives one bounded tool-use loop: send the
// conversation, and if Claude asks for a tool, execute it locally and
// send the result back - up to maxAssistantIterations round trips,
// same "cap it so a stuck loop can't run forever" reasoning as every
// other bounded retry in this codebase.
const maxAssistantIterations = 6

func runAssistantLoop(history []anthropicMessage, callerAddress string) (string, error) {
	messages := make([]anthropicMessage, len(history))
	copy(messages, history)

	for i := 0; i < maxAssistantIterations; i++ {
		resp, err := callAnthropic(anthropicRequest{
			Model:     anthropicAssistantModel,
			MaxTokens: 1024,
			System:    assistantSystemPrompt,
			Messages:  messages,
			Tools:     assistantTools,
		})
		if err != nil {
			return "", err
		}

		if resp.StopReason != "tool_use" {
			var text string
			for _, block := range resp.Content {
				if block.Type == "text" {
					text += block.Text
				}
			}
			return text, nil
		}

		messages = append(messages, anthropicMessage{Role: "assistant", Content: resp.Content})

		var toolResults []anthropicContentBlock
		for _, block := range resp.Content {
			if block.Type != "tool_use" {
				continue
			}
			result, isError := executeAssistantTool(block.Name, block.Input, callerAddress)
			toolResults = append(toolResults, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: block.ID,
				Content:   result,
				IsError:   isError,
			})
		}
		messages = append(messages, anthropicMessage{Role: "user", Content: toolResults})
	}

	return "", fmt.Errorf("ai: assistant did not finish within the iteration limit")
}

// --- HTTP handler --------------------------------------------------

type aiChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiChatRequestData struct {
	Messages []aiChatMessage `json:"messages"`
}

// HandleAIChat is signed (like /session/login-ping) purely so the
// assistant can resolve "my wallet" to a real, verified address
// without the client having to pass one - it never records a
// transaction, since a chat turn isn't an economic event any other
// node needs to agree on (same node-local reasoning as
// HubNotification/NegotiationMessage). The client sends the full
// message history each turn (the Anthropic API is itself stateless);
// nothing is persisted server-side between requests.
func HandleAIChat(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)

	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	if !aiConfigured() {
		http.Error(w, "the AI assistant is not configured on this node", http.StatusServiceUnavailable)
		return
	}

	var req aiChatRequestData

	_, address, err := requireSignedRequest(r, &req)
	if err != nil {
		writeUnauthorized(w, err)
		return
	}

	if len(req.Messages) == 0 {
		http.Error(w, "messages must not be empty", http.StatusBadRequest)
		return
	}
	if len(req.Messages) > 40 {
		http.Error(w, "too many messages in this conversation - start a new chat", http.StatusBadRequest)
		return
	}

	history := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			http.Error(w, "message role must be 'user' or 'assistant'", http.StatusBadRequest)
			return
		}
		if len(m.Content) > 4000 {
			http.Error(w, "a single message must be under 4000 characters", http.StatusBadRequest)
			return
		}
		history = append(history, anthropicMessage{
			Role:    m.Role,
			Content: []anthropicContentBlock{{Type: "text", Text: m.Content}},
		})
	}

	reply, err := runAssistantLoop(history, address)
	if err != nil {
		http.Error(w, "the assistant is temporarily unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"reply": reply,
	})
}
