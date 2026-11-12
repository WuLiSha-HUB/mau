package session

// ToolOfficeState 是一张工具工单的观测态。
type ToolOfficeState struct {
	// Status 工单状态（0=Open 1=Work 2=Closed 3=Timeout）
	Status int
	// ClaimFrame 认领帧号（0 = 未认领）
	ClaimFrame int64
	// Result 回执文本（Closed 时有效）
	Result string
	// HasResult 回执 result 键是否存在
	HasResult bool
}

// ToolOffice 是工具工单面（OA 消费侧窄接口——组合根适配，会话层不依赖运行时实现）。
//
// 语义对齐 C# ToolOrderDog：Post 上架（type=TOOL / name / args 载荷）→ 组 Flow 认领执行 →
// Tick 轮询四态；未注册的组无人认领 → 120s 后 Timeout（与 C# 组未加载同构）。
type ToolOffice interface {
	// Post 上架一单，返回工单号；失败给 ERR 文本（不静默）。
	Post(name, argsJSON string, timeoutFrames int64) (officeID int64, errText string)
	// State 读工单观测态。
	State(officeID int64) ToolOfficeState
}

// pollOffice 轮询单张工单——认领时刻观测 / 闭合取回执 / 超时置标志。
//
// 计时口径对齐 C# ToolOrderDog：ClaimedAtMs 取观测值；快单（认领与闭合同帧）按
// ClaimFrame>0 回填 ClosedAtMs；未观测到的时刻保持 0（不造值）。
func (s *Session) pollOffice(office ToolOffice, dog *toolDog) {
	if office == nil || dog == nil || dog.IsClosed || dog.OfficeID <= 0 {
		return
	}
	st := office.State(dog.OfficeID)
	observedClaim := st.ClaimFrame > 0
	switch st.Status {
	case 2: // Closed
		if st.HasResult {
			dog.Result = st.Result
		} else {
			dog.Result = "ERR|EMPTY_RESULT|工单回执无 result"
			logAt(LogModuleHost, 2, "工具 "+dog.Name+" 回执缺 result——诚实 ERR", LogCategoryTool)
		}
		dog.IsClosed = true
		dog.ClosedAtMs = nowMillis()
		if observedClaim && dog.ClaimedAtMs <= 0 {
			dog.ClaimedAtMs = dog.ClosedAtMs
		}
	case 3: // Timeout
		dog.IsTimedOut = true
		dog.IsClosed = true
		dog.ClosedAtMs = nowMillis()
	default:
		if observedClaim && dog.ClaimedAtMs <= 0 {
			dog.ClaimedAtMs = nowMillis()
		}
	}
}
