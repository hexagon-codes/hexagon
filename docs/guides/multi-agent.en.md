<div align="right">Language: <a href="multi-agent.md">中文</a> | English</div>

# Multi-Agent Collaboration Guide

Hexagon currently provides four in-process multi-agent primitives: `Team` for fixed orchestration, `ParallelAgent` for parallel fan-out, `SwarmRunner` for tool-driven agent transfers, and `AgentNetwork` for messaging by agent ID. Use `ConsensusProtocol` to vote across multiple agent answers. For cross-service communication, use the server and client in `agent/a2a`.

In this guide, variables such as `researcher`, `writer`, `reviewer`, and `manager` represent `agent.Agent` instances with an LLM already configured. For example:

```go
researcher := agent.NewReAct(
    agent.WithName("researcher"),
    agent.WithRole(agent.Role{
        Name:      "Researcher",
        Goal:      "Collect and verify facts",
        Backstory: "You are a rigorous technical researcher.",
    }),
    agent.WithLLM(provider),
)
```

## Choosing a Collaboration Primitive

| Requirement | Primitive | Key behavior |
|-------------|-----------|--------------|
| Fixed order, manager coordination, collaborative aggregation, or round robin | `Team` | Accepts one `agent.Input` and runs members in the configured mode |
| Send the same input to multiple agents concurrently | `ParallelAgent` | Runs agents concurrently and merges successful results |
| Let the model choose the next agent | `TransferTo` + `SwarmRunner` | Switches the current agent after the model calls a transfer tool |
| Send directly by ID or broadcast | `AgentNetwork` | Delivers messages to registered node inboxes or the asynchronous router |
| Vote across multiple agents | `ConsensusProtocol` | Queries online agents concurrently and calculates a consensus result |
| Call across processes or languages | `agent/a2a` | HTTP + JSON-RPC task API |

## Team: Fixed Team Orchestration

### Creating and Running a Team

The first argument to `NewTeam` is the team name. Members and execution mode are configured with options. Execution always accepts `agent.Input`: `Query` contains the task text and `Context` carries optional supplemental data.

```go
team := agent.NewTeam(
    "content-team",
    agent.WithTeamDescription("Research, writing, and review"),
    agent.WithAgents(researcher, writer, reviewer),
    agent.WithMode(agent.TeamModeSequential),
)

output, err := team.Run(ctx, agent.Input{
    Query: "Write a technical overview of RAG",
    Context: map[string]any{
        "audience": "Go developers",
    },
})
if err != nil {
    return err
}
fmt.Println(output.Content)
```

### The Four Modes

| Mode | Execution semantics | Configuration |
|------|---------------------|---------------|
| `TeamModeSequential` | Runs members in order; the previous `Content` becomes the next `Query`, and the previous `Metadata` becomes the next `Context` | Default, or `WithMode` |
| `TeamModeHierarchical` | The manager produces guidance, each member processes the original task, and the manager summarizes successful results | `WithManager(manager)`, which also selects this mode |
| `TeamModeCollaborative` | All members process the same input concurrently and successful outputs are concatenated | `WithMode` |
| `TeamModeRoundRobin` | Cycles through members, feeding each output into the next input; stops when `Metadata["done"]` is `true` or the maximum round count is reached | `WithMode` + `WithMaxRounds` |

Hierarchical mode requires both a manager and at least one member:

```go
team := agent.NewTeam(
    "review-team",
    agent.WithAgents(researcher, writer, reviewer),
    agent.WithManager(manager),
)

output, err := team.Run(ctx, agent.Input{Query: "Review this technical proposal"})
```

`Team` does not have separate parallel or consensus modes. Use `ParallelAgent` for fan-out and `ConsensusProtocol` for voting.

## ParallelAgent: Parallel Fan-Out

`ParallelAgent` sends the same input to all child agents concurrently. Its default merge function concatenates non-empty `Content` in member order. If some children fail, it still returns the successful results and records `failed` and `errors` in `Output.Metadata`. It returns an error only when every child fails.

```go
parallel := agent.NewParallelAgent(
    "independent-review",
    []agent.Agent{securityReviewer, apiReviewer, testReviewer},
    agent.WithMaxParallel(2),
)

output, err := parallel.Run(ctx, agent.Input{
    Query: "Independently review this API change",
})
if err != nil {
    return err
}

failed, _ := output.Metadata["failed"].(int)
fmt.Printf("failed=%d\n%s\n", failed, output.Content)
```

Pass `WithMergeFunc` when the results require custom aggregation:

```go
parallel := agent.NewParallelAgent(
    "review-summary",
    []agent.Agent{securityReviewer, apiReviewer},
    agent.WithMergeFunc(func(outputs []agent.Output) agent.Output {
        parts := make([]string, 0, len(outputs))
        for _, output := range outputs {
            if output.Content != "" {
                parts = append(parts, output.Content)
            }
        }
        return agent.Output{Content: strings.Join(parts, "\n---\n")}
    }),
)
```

## TransferTo and SwarmRunner: Model-Driven Transfers

`TransferTo(target)` creates a real LLM tool. After it is added to the current agent's tools, the model can call it with `message`, `reason`, and an optional `context`. `SwarmRunner` reads the transfer from the output's tool-call records, passes `message` and `context` as the target agent's next input, and continues execution.

```go
billing := agent.NewReAct(
    agent.WithName("billing"),
    agent.WithDescription("Handle billing questions"),
    agent.WithLLM(provider),
)

frontDesk := agent.NewReAct(
    agent.WithName("front-desk"),
    agent.WithDescription("Identify the user's request"),
    agent.WithLLM(provider),
    agent.WithTools(agent.TransferTo(billing)),
)

runner := agent.NewSwarmRunner(frontDesk)
runner.MaxHandoffs = 4

output, err := runner.Run(ctx, agent.Input{
    Query: "Explain the duplicate charge on this month's bill",
})
if err != nil {
    return err
}
fmt.Println(output.Content)
```

Registering the transfer tool does not trigger a transfer by itself. A `Handoff` appears only when the current agent's model actually calls the tool. Exceeding `MaxHandoffs` returns an error.

## AgentNetwork: In-Process Messaging

### Registration and Direct Sending

Create a network and register agents before sending. Registering the same agent ID twice returns an error.

```go
network := agent.NewAgentNetwork(
    "review-network",
    agent.WithNetworkTopology(agent.TopologyMesh),
    agent.WithNetworkInboxSize(128),
)

if err := network.Register(sender); err != nil {
    return err
}
if err := network.Register(receiver); err != nil {
    return err
}
```

`RegisterHandler` binds a handler to a message `Topic`. The convenience message created by `SendTo` has an empty topic. To process it through a handler, register the `""` topic and start the network router.

```go
network.RegisterHandler("", func(
    ctx context.Context,
    msg *agent.NetworkMessage,
) (*agent.NetworkMessage, error) {
    fmt.Printf("%s -> %s: %v\n", msg.From, msg.To, msg.Content)
    return nil, nil
})

networkCtx, stopNetwork := context.WithCancel(context.Background())
defer stopNetwork()

if err := network.Start(networkCtx); err != nil {
    return err
}
defer network.Stop()

if err := network.SendTo(
    ctx,
    sender.ID(),
    receiver.ID(),
    "Review this result",
); err != nil {
    return err
}
```

After the network starts, a successful `SendTo` means that the message entered the asynchronous router queue; it does not mean that the handler has finished. Before the network starts, the message is delivered directly to the target node's `Inbox` and registered handlers are not invoked.

### Broadcast

`Broadcast` directly delivers a copy to every registered node except the sender:

```go
if err := network.Broadcast(
    ctx,
    sender.ID(),
    "Start a new review round",
); err != nil {
    return err
}
```

Broadcasts use node inboxes and do not pass through `RegisterHandler`. Sending may fail when an inbox is full or closed, a node is offline, or the context is canceled.

## ConsensusProtocol: Voting

`ConsensusProtocol` uses registered, online agents in the network as voters. `Propose` calls those agents concurrently. It does not depend on `AgentNetwork.Start`, because voting calls registered agents directly.

```go
network := agent.NewAgentNetwork("architecture-vote")
for _, voter := range []agent.Agent{expertA, expertB, expertC} {
    if err := network.Register(voter); err != nil {
        return err
    }
}

protocol := agent.NewConsensusProtocol(
    network,
    agent.WithConsensusStrategy(agent.ConsensusMajority),
    agent.WithConsensusThreshold(2.0/3.0),
    agent.WithMinParticipation(2.0/3.0),
    agent.WithConsensusTimeout(15*time.Second),
)

result, err := protocol.Propose(
    ctx,
    "Should this architecture proposal be accepted?",
    []any{"accept", "reject"},
)
if err != nil {
    return err
}

if !result.Reached {
    fmt.Printf("no consensus: %s\n", result.Reason)
} else {
    fmt.Printf("decision=%v confidence=%.2f\n", result.Decision, result.Confidence)
}
```

The vote value is derived by exact or substring matching between each agent response and the available options. A timeout or individual agent failure reduces participation. Always inspect `Reached`, `Participation`, and `Reason` rather than reading only `Decision`.

## StateManager: Four State Layers

`StateManager` provides Turn, Session, Agent, and Global state. It differs from `agent.Input.Context`: `Input.Context` is supplemental data for one call, while the state manager can retain values across turns, create snapshots, and restore them.

```go
global := agent.NewGlobalState()
state := agent.NewStateManager("session-42", global)

state.Turn().Set("draft", "v1")
state.Session().Set("user_id", "u-123")
state.Agent().Set("preferred_style", "concise")
state.Global().Set("release", "v0.6.0")

if userID, ok := state.Session().Get("user_id"); ok {
    fmt.Println(userID)
}

snapshot := state.Snapshot()
state.NewTurn()

if err := state.Restore(snapshot); err != nil {
    return err
}

ctx = agent.ContextWithStateManager(ctx, state)
current := agent.StateManagerFromContext(ctx)
if current == nil {
    return errors.New("state manager missing")
}
```

Explicitly reuse the same `GlobalState` when multiple state managers need to share application-level data. `NewTurn` resets Turn state and increments the Session turn count.

## Timeouts and Cancellation

`Team`, `ParallelAgent`, `SwarmRunner`, network sends, and the A2A client all accept a caller-provided `context.Context`. Set a deadline at the entry point and preserve normal Go context error checks:

```go
runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
defer cancel()

output, err := team.Run(runCtx, agent.Input{Query: "Complete this review round"})
if err != nil {
    switch {
    case errors.Is(err, context.DeadlineExceeded):
        return fmt.Errorf("team timed out: %w", err)
    case errors.Is(err, context.Canceled):
        return fmt.Errorf("team canceled: %w", err)
    default:
        return err
    }
}
_ = output
```

Underlying agents and providers must also honor the context for timeouts to stop real work promptly. `WithConsensusTimeout` separately limits vote collection, but a voting timeout normally appears as insufficient participation rather than a `Propose` error.

## A2A: Cross-Service Task Calls

`agent/a2a` provides a standalone server and client. The A2A client is not an `agent.Agent` adapter and cannot be added directly to a `Team`. Applications use `a2a.Client` to query the Agent Card, submit tasks, and query task state.

### Starting a Server

```go
card := &a2a.AgentCard{
    Name:               "remote-reviewer",
    Description:        "Remote review service",
    URL:                "http://localhost:8080",
    Version:            "0.1.0",
    DefaultInputModes:  []string{"text"},
    DefaultOutputModes: []string{"text"},
}

server := a2a.NewServer(card, a2a.NewEchoHandler())
if err := server.Start(":8080"); err != nil {
    log.Fatal(err)
}
```

`Start` blocks until the server is shut down or listening fails. If application lifecycle code starts it in a separate goroutine, shut it down with a deadline-bound `server.Stop(ctx)` during process exit.

### Querying and Submitting Tasks

```go
client := a2a.NewClient(
    "http://localhost:8080",
    a2a.WithTimeout(10*time.Second),
)
defer client.Close()

queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()

card, err := client.GetAgentCard(queryCtx)
if err != nil {
    return err
}
fmt.Println(card.Name)

task, err := client.SendMessage(queryCtx, &a2a.SendMessageRequest{
    Message: a2a.NewUserMessage("Review this proposal"),
})
if err != nil {
    return err
}

stored, err := client.GetTask(queryCtx, task.ID)
if err != nil {
    return err
}
fmt.Println(stored.Status.State)
```

### Error Handling

A2A JSON-RPC errors implement `error`. Use `GetA2AError` to read protocol error codes. Network and context failures remain regular Go error chains:

```go
_, err := client.GetTask(queryCtx, "missing-task")
if err != nil {
    if protocolErr := a2a.GetA2AError(err); protocolErr != nil {
        if protocolErr.Code == a2a.CodeTaskNotFound {
            return fmt.Errorf("remote task not found: %w", err)
        }
        return fmt.Errorf("A2A error %d: %w", protocolErr.Code, err)
    }
    if errors.Is(err, context.DeadlineExceeded) {
        return fmt.Errorf("A2A query timed out: %w", err)
    }
    return err
}
```

## Operational Boundaries

- When using `TeamModeRoundRobin`, bound the rounds with `WithMaxRounds`; configure `ParallelAgent` with a resource-appropriate `WithMaxParallel`, and bound `SwarmRunner.MaxHandoffs`.
- Inspect `Output.Metadata` for partial `ParallelAgent` failures; inspect both `Reached` and `Participation` for consensus results.
- Register network nodes before sending. Successful asynchronous sending does not mean business processing has completed.
- Propagate cancellation and deadlines through context instead of creating background work that the caller cannot cancel.
- At service boundaries, handle A2A protocol errors, HTTP/network errors, and context errors separately.

## Next Steps

- Read the [A2A Protocol Guide](./a2a-protocol.en.md)
- Read [Graph Orchestration Best Practices](./graph-orchestration.en.md)
- Read the [Performance Optimization Guide](./performance-optimization.en.md)
- Read the [Observability Integration Guide](./observability.en.md)
