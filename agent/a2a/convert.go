package a2a

import (
	"maps"

	"github.com/hexagon-codes/hexagon/agent"
)

// ============== AgentInfo <-> AgentCard 转换 ==============

// AgentInfoToCard 将 Hexagon AgentInfo 转换为 A2A AgentCard
//
// 转换规则:
//   - ID -> URL 参数（需要外部提供完整 URL）
//   - Name -> Name
//   - Description -> Description
//   - Version -> Version
//   - Tags -> Skills.Tags
//   - Capabilities -> Skills (名称转换为技能)
//   - Endpoint -> URL
func AgentInfoToCard(info *agent.AgentInfo, baseURL string) *AgentCard {
	if info == nil {
		return nil
	}

	card := &AgentCard{
		Name:        info.Name,
		Description: info.Description,
		URL:         baseURL,
		Version:     info.Version,
		Capabilities: AgentCapabilities{
			Streaming: true,
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}

	// 转换能力为技能
	if len(info.Capabilities) > 0 {
		card.Skills = make([]AgentSkill, 0, len(info.Capabilities))
		for _, cap := range info.Capabilities {
			skill := AgentSkill{
				ID:          cap.Name,
				Name:        cap.Name,
				Description: cap.Description,
			}
			card.Skills = append(card.Skills, skill)
		}
	}

	// 添加标签到技能
	if len(info.Tags) > 0 && len(card.Skills) == 0 {
		// 如果没有能力但有标签，创建一个默认技能
		card.Skills = []AgentSkill{
			{
				ID:   "default",
				Name: info.Name,
				Tags: info.Tags,
			},
		}
	} else if len(card.Skills) > 0 {
		// 将标签添加到所有技能
		for i := range card.Skills {
			card.Skills[i].Tags = info.Tags
		}
	}

	return card
}

// CardToAgentInfo 将 A2A AgentCard 转换为 Hexagon AgentInfo
//
// 转换规则:
//   - URL -> Endpoint
//   - Name -> Name
//   - Description -> Description
//   - Version -> Version
//   - Skills -> Capabilities
func CardToAgentInfo(card *AgentCard) *agent.AgentInfo {
	if card == nil {
		return nil
	}

	info := &agent.AgentInfo{
		Name:        card.Name,
		Description: card.Description,
		Version:     card.Version,
		Endpoint:    card.URL,
	}

	// 从技能收集标签
	tagSet := make(map[string]bool)
	for _, skill := range card.Skills {
		for _, tag := range skill.Tags {
			tagSet[tag] = true
		}
	}
	for tag := range tagSet {
		info.Tags = append(info.Tags, tag)
	}

	// 转换技能为能力
	if len(card.Skills) > 0 {
		info.Capabilities = make([]agent.Capability, 0, len(card.Skills))
		for _, skill := range card.Skills {
			cap := agent.Capability{
				Name:        skill.ID,
				Description: skill.Description,
			}
			info.Capabilities = append(info.Capabilities, cap)
		}
	}

	return info
}

// ============== Message 转换 ==============

// AgentInputToMessage 将 Hexagon Agent Input 转换为 A2A Message
func AgentInputToMessage(input agent.Input) Message {
	return Message{
		Role: RoleUser,
		Parts: []Part{
			&TextPart{Text: input.Query},
		},
		Metadata: input.Context,
	}
}

// MessageToAgentInput 将 A2A Message 转换为 Hexagon Agent Input
func MessageToAgentInput(msg *Message) agent.Input {
	return agent.Input{
		Query:   msg.GetTextContent(),
		Context: msg.Metadata,
	}
}

// AgentOutputToMessage 将 Hexagon Agent Output 转换为 A2A Message
func AgentOutputToMessage(output agent.Output) Message {
	parts := make([]Part, 0)

	// 主要内容
	if output.Content != "" {
		parts = append(parts, &TextPart{Text: output.Content})
	}

	// 元数据中的附加信息
	metadata := make(map[string]any)
	if output.ToolCalls != nil {
		metadata["toolCalls"] = output.ToolCalls
	}
	// Usage 是值类型，通过 TotalTokens 判断是否有有效值
	if output.Usage.TotalTokens > 0 {
		metadata["usage"] = output.Usage
	}
	if output.Metadata != nil {
		maps.Copy(metadata, output.Metadata)
	}

	return Message{
		Role:     RoleAgent,
		Parts:    parts,
		Metadata: metadata,
	}
}

// MessageToAgentOutput 将 A2A Message 转换为 Hexagon Agent Output
func MessageToAgentOutput(msg *Message) agent.Output {
	return agent.Output{
		Content:  msg.GetTextContent(),
		Metadata: msg.Metadata,
	}
}

// ============== Artifact 转换 ==============

// AgentOutputToArtifact 将 Agent Output 转换为 Artifact
func AgentOutputToArtifact(output agent.Output, name string) Artifact {
	parts := make([]Part, 0)

	if output.Content != "" {
		parts = append(parts, &TextPart{Text: output.Content})
	}

	return Artifact{
		Name:  name,
		Parts: parts,
	}
}

// ============== 状态转换 ==============

// AgentStatusToTaskState 将 Hexagon Agent Status 转换为 A2A TaskState
func AgentStatusToTaskState(status agent.AgentStatus) TaskState {
	switch status {
	case agent.StatusHealthy:
		return TaskStateWorking
	case agent.StatusUnhealthy, agent.StatusOffline:
		return TaskStateFailed
	case agent.StatusDraining:
		return TaskStateCanceled
	default:
		return TaskStateSubmitted
	}
}
