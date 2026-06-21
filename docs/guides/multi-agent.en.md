<div align="right">Language: <a href="multi-agent.md">中文</a> | English</div>

# Multi-Agent Collaboration Guide

Hexagon provides powerful multi-agent collaboration capabilities, including team cooperation, agent handoffs, and network communication.

## Role System

### Defining a Role

```go
import "github.com/hexagon-codes/hexagon/agent"

role := agent.Role{
    Name:      "Researcher",
    Goal:      "Deeply research technical problems and provide detailed analysis",
    Backstory: "You are an experienced technical researcher, skilled at analyzing complex technical issues.",
}

researcher := agent.NewBaseAgent(
    agent.WithRole(role),
    agent.WithLLM(provider),
)
```

### Role Best Practices

**A good role definition**:
```go
role := agent.Role{
    Name: "Customer Support Specialist",
    Goal: "Respond to customer issues quickly and provide professional solutions",
    Backstory: `You are an experienced customer support specialist with 5 years of experience.
You are familiar with all company products and services, can quickly pinpoint problems and provide solutions.
Your communication style is friendly and professional, and you always think from the customer's perspective.`,
}
```

**Key points**:
- Name: Clear and descriptive role name
- Goal: Specific, measurable objective
- Backstory: Rich background story to enhance the role's persona

## Team Collaboration

### Work Modes

#### 1. Sequential Mode

Agents execute tasks one after another in order.

```go
team := agent.NewTeam(
    agent.WithTeamName("research-team"),
    agent.WithTeamMode(agent.TeamModeSequential),
    agent.WithTeamAgents(researcher, writer, reviewer),
)

result, _ := team.Run(ctx, agent.TeamInput{
    Task: "Research and write a technical article about RAG",
})
```

**Use case**: Pipeline-style tasks, e.g., Research → Write → Review

#### 2. Hierarchical Mode

A Manager Agent coordinates the other agents.

```go
manager := agent.NewBaseAgent(
    agent.WithRole(agent.Role{
        Name: "Project Manager",
        Goal: "Coordinate the team to complete the project",
    }),
)

team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeHierarchical),
    agent.WithTeamManager(manager),
    agent.WithTeamAgents(researcher, developer, tester),
)
```

**Use case**: Dynamic task assignment and coordination

#### 3. Consensus Mode

All agents vote to reach a consensus.

```go
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeConsensus),
    agent.WithTeamAgents(expert1, expert2, expert3),
    agent.WithConsensusThreshold(0.67), // passes with 67% agreement
)
```

**Use case**: Decision-making tasks that require multiple expert opinions

#### 4. Parallel Mode

Agents run in parallel and results are merged at the end.

```go
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeParallel),
    agent.WithTeamAgents(agent1, agent2, agent3),
)
```

**Use case**: Independent subtasks that can be processed concurrently

### Complete Example

```go
package main

import (
    "context"
    "fmt"

    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/ai-core/llm/openai"
)

func main() {
    provider := openai.New("your-api-key")

    // Define agents
    researcher := agent.NewBaseAgent(
        agent.WithName("researcher"),
        agent.WithRole(agent.Role{
            Name:      "Researcher",
            Goal:      "Research deeply and gather information",
            Backstory: "You are a technical research expert",
        }),
        agent.WithLLM(provider),
    )

    writer := agent.NewBaseAgent(
        agent.WithName("writer"),
        agent.WithRole(agent.Role{
            Name:      "Writer",
            Goal:      "Organize research findings into an article",
            Backstory: "You are a professional technical writer",
        }),
        agent.WithLLM(provider),
    )

    reviewer := agent.NewBaseAgent(
        agent.WithName("reviewer"),
        agent.WithRole(agent.Role{
            Name:      "Reviewer",
            Goal:      "Review the quality of the article",
            Backstory: "You are a senior editor",
        }),
        agent.WithLLM(provider),
    )

    // Create team
    team := agent.NewTeam(
        agent.WithTeamName("content-team"),
        agent.WithTeamMode(agent.TeamModeSequential),
        agent.WithTeamAgents(researcher, writer, reviewer),
    )

    // Execute task
    ctx := context.Background()
    result, err := team.Run(ctx, agent.TeamInput{
        Task: "Write a technical article about AI Agents",
    })

    if err != nil {
        panic(err)
    }

    fmt.Println(result.Output)
}
```

## Agent Handoff

Agents can hand off tasks to one another.

### Basic Handoff

```go
// Agent A executes a task and hands it off to Agent B
handoff := agent.NewHandoff(
    agent.WithHandoffFrom(agentA),
    agent.WithHandoffTo(agentB),
    agent.WithHandoffCondition(func(result agent.Output) bool {
        // Hand off when the condition is met
        return result.Status == "need_review"
    }),
)

result, _ := handoff.Execute(ctx, input)
```

### Routed Handoff

Dynamically select the handoff target based on results.

```go
router := agent.NewRouter(
    agent.WithRoutes(map[string]agent.Agent{
        "technical": techAgent,
        "business":  bizAgent,
        "general":   generalAgent,
    }),
    agent.WithRouteDecision(func(ctx context.Context, input agent.Input) (string, error) {
        // Use LLM to decide routing
        // Returns "technical", "business", or "general"
    }),
)
```

### Loop Handoff

Multiple agents collaborate in a loop until completion.

```go
loop := agent.NewHandoffLoop(
    agent.WithLoopAgents(coder, reviewer, tester),
    agent.WithMaxIterations(5),
    agent.WithStopCondition(func(result agent.Output) bool {
        return result.Metadata["tests_passed"] == true
    }),
)
```

## Agent Network Communication

### Point-to-Point Communication

```go
// Agent A sends a message to Agent B
agentA.Send(ctx, agentB, agent.Message{
    Type:    "request",
    Content: "Please help me analyze this problem",
    Data: map[string]any{
        "problem": problem,
    },
})

// Agent B receives messages
msgChan := agentB.Receive(ctx)
for msg := range msgChan {
    // Process the message
    response := processMessage(msg)

    // Reply
    agentB.Send(ctx, agentA, response)
}
```

### Broadcast Communication

```go
network := agent.NewNetwork(
    agent.WithNetworkAgents(agent1, agent2, agent3),
)

// Broadcast a message to all agents
network.Broadcast(ctx, agent.Message{
    Type:    "announcement",
    Content: "Starting new task",
})
```

### Pub/Sub Pattern

```go
// Agents subscribe to a specific topic
network.Subscribe(agent1, "data_updated")
network.Subscribe(agent2, "data_updated")

// Publish a message
network.Publish(ctx, "data_updated", agent.Message{
    Content: "Data has been updated",
    Data: map[string]any{
        "version": "1.2",
    },
})
```

## Consensus Mechanism

Multiple agents negotiate to reach a consensus.

### Voting Consensus

```go
consensus := agent.NewConsensus(
    agent.WithConsensusAgents(agent1, agent2, agent3),
    agent.WithConsensusStrategy(agent.ConsensusStrategyVoting),
    agent.WithConsensusThreshold(0.67), // requires 67% agreement
)

decision, _ := consensus.Decide(ctx, agent.ConsensusInput{
    Proposal: "Should we adopt the new technical solution?",
    Options:  []string{"Agree", "Reject", "Need more information"},
})

fmt.Println(decision.Result) // "Agree" / "Reject" / "Need more information"
```

### Weighted Voting

```go
consensus := agent.NewConsensus(
    agent.WithConsensusWeights(map[string]float64{
        "senior_expert": 2.0, // senior expert weight: 2
        "junior_expert": 1.0, // junior expert weight: 1
    }),
)
```

## State Management

### Shared State

```go
// Global state
globalState := agent.NewGlobalState()
globalState.Set("project_status", "in_progress")

// Agents access the global state
status := globalState.Get("project_status")
```

### Session State

```go
// Session-level state
session := agent.NewSession()
session.Set("user_id", "123")
session.Set("conversation_history", []string{})

// Agent uses the session
result, _ := agentA.RunWithSession(ctx, input, session)
```

## Best Practices

### 1. Clear Role Division

```go
// ✅ Good practice
researcher := agent.NewBaseAgent(
    agent.WithRole(agent.Role{
        Name: "Data Analyst",
        Goal: "Analyze data and generate reports",
    }),
)

writer := agent.NewBaseAgent(
    agent.WithRole(agent.Role{
        Name: "Technical Writer",
        Goal: "Translate analysis results into accessible articles",
    }),
)

// ❌ Bad practice
generic := agent.NewBaseAgent(
    agent.WithRole(agent.Role{
        Name: "Assistant",
        Goal: "Do various things",
    }),
)
```

### 2. Appropriate Team Size

- 2–5 agents: Optimal size
- 5–10 agents: Requires stronger coordination
- 10+ agents: Consider hierarchical or grouped structures

### 3. Avoid Circular Dependencies

```go
// ❌ Bad practice
agentA → agentB → agentC → agentA // circular

// ✅ Good practice
agentA → agentB → agentC // unidirectional
```

### 4. Timeouts and Retries

```go
team := agent.NewTeam(
    agent.WithTeamTimeout(5 * time.Minute),
    agent.WithTeamRetryPolicy(&agent.RetryPolicy{
        MaxRetries: 3,
        BackoffStrategy: agent.ExponentialBackoff,
    }),
)
```

### 5. Monitoring and Observability

```go
import "github.com/hexagon-codes/hexagon/observe"

// Add observers to the team
team.OnAgentStart(func(agentName string) {
    fmt.Printf("Agent %s started\n", agentName)
})

team.OnAgentComplete(func(agentName string, result agent.Output) {
    fmt.Printf("Agent %s completed\n", agentName)
})

team.OnAgentError(func(agentName string, err error) {
    fmt.Printf("Agent %s error: %v\n", agentName, err)
})
```

## Real-World Examples

### Example 1: Content Creation Team

```go
// Researcher → Writer → Reviewer → Publisher
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeSequential),
    agent.WithTeamAgents(
        newResearcher(),
        newWriter(),
        newReviewer(),
        newPublisher(),
    ),
)
```

### Example 2: Customer Service System

```go
// A router agent dispatches to specialist agents based on issue type
router := agent.NewRouter(
    agent.WithRoutes(map[string]agent.Agent{
        "technical_support": techAgent,
        "billing":           billingAgent,
        "general":           generalAgent,
    }),
)
```

### Example 3: Code Review

```go
// Multiple reviewers review in parallel and reach a consensus
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeConsensus),
    agent.WithTeamAgents(
        seniorReviewer1,
        seniorReviewer2,
        juniorReviewer,
    ),
    agent.WithConsensusThreshold(0.67),
)
```

## Debugging Tips

```go
// Enable team debug mode
team.SetDebug(true)

// Observe agent interactions
team.OnMessage(func(from, to string, msg agent.Message) {
    fmt.Printf("%s → %s: %s\n", from, to, msg.Content)
})

// Observe the decision process
consensus.OnVote(func(agent string, vote string) {
    fmt.Printf("%s voted: %s\n", agent, vote)
})
```

## A2A Protocol: Cross-Framework Agent Collaboration

Hexagon supports Google's A2A (Agent-to-Agent) protocol, enabling your agents to communicate in a standardized way with agents built on other frameworks or in other languages.

### What Is the A2A Protocol?

A2A is an open protocol that defines a standard way for AI agents to communicate:

- **Agent Card**: Describes agent capabilities (`.well-known/agent-card.json`)
- **Task**: Task lifecycle management (submit → execute → complete)
- **Message**: Multi-modal messages (text, files, data)
- **Streaming**: Real-time streaming responses
- **Push Notification**: Task status push notifications

### A2A vs. Hexagon Multi-Agent

| Scenario | Approach |
|----------|----------|
| Same-process agent collaboration | Use Team, Network, Handoff |
| Cross-service agent collaboration | Use the A2A protocol |
| Mixed scenarios | Combine both |

### Exposing an Agent as an A2A Service

```go
import (
    "github.com/hexagon-codes/hexagon/agent"
    "github.com/hexagon-codes/hexagon/agent/a2a"
)

// Create a Hexagon Agent
myAgent := agent.NewBaseAgent(
    agent.WithName("assistant"),
    agent.WithRole(agent.Role{
        Name: "Assistant",
        Goal: "Help users solve problems",
    }),
    agent.WithLLM(provider),
)

// Expose it as an A2A service
server := a2a.ExposeAgent(myAgent, ":8080")
defer server.Stop(ctx)
```

### Connecting to a Remote A2A Agent

```go
// Connect to a remote A2A agent
remoteAgent := a2a.ConnectToA2AAgent("http://remote-agent:8080")

// Use it just like a local agent
result, _ := remoteAgent.Run(ctx, agent.Input{
    Messages: []llm.Message{{Role: "user", Content: "Hello"}},
})
```

### Hybrid Team: Local + Remote Agents

```go
// Local agent
localAgent := agent.NewBaseAgent(...)

// Remote A2A agents
remoteAgent1 := a2a.ConnectToA2AAgent("http://python-agent:8080")
remoteAgent2 := a2a.ConnectToA2AAgent("http://nodejs-agent:8080")

// Form a hybrid team
team := agent.NewTeam(
    agent.WithTeamMode(agent.TeamModeSequential),
    agent.WithTeamAgents(localAgent, remoteAgent1, remoteAgent2),
)

// Execute task
result, _ := team.Run(ctx, agent.TeamInput{
    Task: "Analyze data and generate a report",
})
```

### A2A Use Cases

1. **Cross-language collaboration**: Go agents collaborating with Python/Node.js agents
2. **Microservice architecture**: Deploying agents as independent services
3. **Multi-team collaboration**: Integrating agents developed by different teams
4. **Third-party agents**: Connecting to external A2A-compatible services

> For more details, see the [A2A Protocol Guide](./a2a-protocol.md)

## Next Steps

- Learn about [Multi-Agent in Graph Orchestration](./graph-orchestration.md#多-agent-节点)
- Explore [Performance Optimization](./performance-optimization.md#多-agent-优化)
- Master [Observability](./observability.md)
