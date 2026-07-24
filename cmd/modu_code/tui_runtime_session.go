package main

import coding_agent "github.com/openmodu/modu/pkg/coding_agent"

type moduTUIRuntimeSession struct {
	*coding_agent.CodingSession
}

func (s moduTUIRuntimeSession) HasQueuedMessages() bool {
	agent := s.GetAgent()
	return agent != nil && agent.HasQueuedMessages()
}
