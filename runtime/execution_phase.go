package runtime

// ExecutionPhase 区分模型前输入采用、在途工具和已完成轮次；同一 Turn 不能混作同一恢复点。
type ExecutionPhase string

const (
	PhaseBeforeModel  ExecutionPhase = "before_model"
	PhasePendingTools ExecutionPhase = "pending_tools"
	PhaseAfterTurn    ExecutionPhase = "after_turn"
	PhaseFinal        ExecutionPhase = "final"
)

func snapshotNextTurn(snapshot Snapshot) int {
	if snapshot.Phase == PhaseBeforeModel {
		return snapshot.Step
	}
	return snapshot.Step + 1
}
