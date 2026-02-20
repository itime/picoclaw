package channels

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
)

type MultiAgentHandler struct {
	discord      *MultiAgentDiscordChannel
	bus          *bus.MessageBus
	agentPrompts map[AgentType]string
}

func NewMultiAgentHandler(discord *MultiAgentDiscordChannel, msgBus *bus.MessageBus) *MultiAgentHandler {
	handler := &MultiAgentHandler{
		discord:      discord,
		bus:          msgBus,
		agentPrompts: defaultAgentPrompts(),
	}

	handler.setupHandlers()
	return handler
}

func defaultAgentPrompts() map[AgentType]string {
	return map[AgentType]string{
		AgentMaster: `You are Master, the lead coordinator of a multi-agent team.
Your role:
- Coordinate work between Dev, QA, PM, and Ops agents
- Make high-level decisions and delegate tasks
- Synthesize information from other agents
- Respond to humans when no specific agent is mentioned

When delegating, use @dev, @qa, @pm, or @ops to address specific agents.
Keep responses concise and actionable.`,

		AgentDev: `You are Dev, the development specialist.
Your role:
- Write and review code
- Implement features and fix bugs
- Explain technical concepts
- Suggest architectural improvements

When you need QA testing, mention @qa. For deployment, mention @ops.
Focus on clean, maintainable code.`,

		AgentQA: `You are QA, the quality assurance specialist.
Your role:
- Review code for bugs and edge cases
- Suggest test scenarios
- Verify implementations meet requirements
- Report issues to @dev

Be thorough but constructive in your feedback.`,

		AgentPM: `You are PM, the product manager.
Your role:
- Clarify requirements and user stories
- Prioritize features and tasks
- Track progress and blockers
- Communicate with stakeholders

Keep the team focused on delivering value.`,

		AgentOps: `You are Ops, the operations specialist.
Your role:
- Handle deployment and infrastructure
- Monitor system health
- Manage configurations
- Respond to incidents

Prioritize stability and reliability.`,
	}
}

func (h *MultiAgentHandler) setupHandlers() {
	for _, agentType := range AllAgentTypes {
		agent := agentType
		h.discord.SetAgentHandler(agent, func(msg ActorMessage) error {
			return h.handleAgentMessage(agent, msg)
		})
	}
}

func (h *MultiAgentHandler) handleAgentMessage(agent AgentType, msg ActorMessage) error {
	logger.InfoCF("multi_agent", "Processing message for agent", map[string]any{
		"agent":      string(agent),
		"from":       string(msg.From),
		"channel_id": msg.ChannelID,
		"content":    truncateForLog(msg.Content, 50),
	})

	conv := h.discord.convManager.GetOrCreateConversation(msg.ChannelID)
	conversationContext := conv.BuildContextForAgent(agent, 10)

	systemPrompt := h.agentPrompts[agent]
	if conversationContext != "" {
		systemPrompt += "\n\n## Recent Conversation:\n" + conversationContext
	}

	var senderInfo string
	if msg.From == "" {
		senderInfo = "Human"
	} else {
		senderInfo = string(msg.From)
	}

	fullContent := fmt.Sprintf("[From %s]: %s", senderInfo, msg.Content)

	sessionKey := fmt.Sprintf("discord_multi:%s:%s", msg.ChannelID, agent)

	inbound := bus.InboundMessage{
		Channel:    "discord_multi",
		SenderID:   string(msg.From),
		ChatID:     msg.ChannelID,
		Content:    fullContent,
		SessionKey: sessionKey,
		Metadata: map[string]string{
			"agent":          string(agent),
			"system_prompt":  systemPrompt,
			"is_multi_agent": "true",
		},
	}

	h.bus.PublishInbound(inbound)

	return nil
}

func (h *MultiAgentHandler) SendAgentResponse(ctx context.Context, agent AgentType, channelID, content string) error {
	mentions := h.parseMentionsFromResponse(content)

	if err := h.discord.SendAsAgent(ctx, agent, channelID, content); err != nil {
		return err
	}

	h.discord.router.HandleAgentResponse(agent, content, channelID)

	if len(mentions) > 0 {
		cleanContent := h.cleanMentionsFromResponse(content)
		for _, target := range mentions {
			if err := h.discord.router.RouteToAgent(agent, target, cleanContent, channelID); err != nil {
				logger.ErrorCF("multi_agent", "Failed to route to agent", map[string]any{
					"from":  string(agent),
					"to":    string(target),
					"error": err.Error(),
				})
			}
		}
	}

	return nil
}

func (h *MultiAgentHandler) parseMentionsFromResponse(content string) []AgentType {
	var mentions []AgentType
	seen := make(map[AgentType]bool)

	for _, agent := range AllAgentTypes {
		pattern := "@" + string(agent)
		if strings.Contains(strings.ToLower(content), pattern) {
			if !seen[agent] {
				seen[agent] = true
				mentions = append(mentions, agent)
			}
		}
	}

	return mentions
}

func (h *MultiAgentHandler) cleanMentionsFromResponse(content string) string {
	result := content
	for _, agent := range AllAgentTypes {
		pattern := "@" + string(agent)
		result = strings.ReplaceAll(strings.ToLower(result), pattern, "")
	}
	return strings.TrimSpace(result)
}

func (h *MultiAgentHandler) SetAgentPrompt(agent AgentType, prompt string) {
	h.agentPrompts[agent] = prompt
}

func (h *MultiAgentHandler) GetAgentPrompt(agent AgentType) string {
	return h.agentPrompts[agent]
}

func (h *MultiAgentHandler) BroadcastSystemMessage(ctx context.Context, channelID, message string) error {
	formattedMsg := fmt.Sprintf("📢 **System**: %s", message)
	return h.discord.SendAsAgent(ctx, AgentMaster, channelID, formattedMsg)
}

func (h *MultiAgentHandler) GetConversationStatus(channelID string) map[string]any {
	conv, exists := h.discord.convManager.GetConversation(channelID)
	if !exists {
		return map[string]any{
			"exists": false,
		}
	}

	return map[string]any{
		"exists":        true,
		"state":         string(conv.GetState()),
		"pending":       conv.GetPendingCount(),
		"active_agents": agentTypesToStrings(conv.GetActiveAgents()),
		"message_count": len(conv.Messages),
	}
}

type ResponseCallback func(agent AgentType, channelID, content string)

type MultiAgentResponseHandler struct {
	handler  *MultiAgentHandler
	callback ResponseCallback
}

func NewMultiAgentResponseHandler(handler *MultiAgentHandler) *MultiAgentResponseHandler {
	return &MultiAgentResponseHandler{
		handler: handler,
	}
}

func (rh *MultiAgentResponseHandler) SetCallback(cb ResponseCallback) {
	rh.callback = cb
}

func (rh *MultiAgentResponseHandler) HandleResponse(agent AgentType, channelID, content string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := rh.handler.SendAgentResponse(ctx, agent, channelID, content); err != nil {
		logger.ErrorCF("multi_agent", "Failed to send agent response", map[string]any{
			"agent": string(agent),
			"error": err.Error(),
		})
	}

	if rh.callback != nil {
		rh.callback(agent, channelID, content)
	}
}
