package runtime

import "github.com/hexagon-codes/ai-core/llm"

func (s *State) appendPartialDraft(text string) {
	if !s.hasPartialDraft {
		s.hasPartialDraft = true
		s.draftMessageStart, s.draftOutputStart = len(s.Messages), len(s.outputMessages)
	}
	s.appendOutputMessage(llm.Message{Role: llm.RoleAssistant, Content: text})
	s.partialText += text
}

// discardPartialDraft 不撤销已经执行的工具、已知用量或失败尝试事件。
func (s *State) discardPartialDraft() string {
	text := s.partialText
	if s.hasPartialDraft {
		if s.draftMessageStart >= 0 && s.draftMessageStart <= len(s.Messages) {
			s.Messages = s.Messages[:s.draftMessageStart]
		}
		if s.draftOutputStart >= 0 && s.draftOutputStart <= len(s.outputMessages) {
			s.outputMessages = s.outputMessages[:s.draftOutputStart]
		}
	}
	s.partialText, s.hasPartialDraft = "", false
	return text
}
