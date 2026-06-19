// Package agent 提供 AI Agent 接口和实现
package agent

// Role 角色定义
// 角色系统：定义 Agent 的 Name/Goal/Backstory。
type Role struct {
	// Name 角色名称
	Name string `yaml:"name" json:"name"`

	// Title 角色头衔 (e.g., "Senior Researcher", "Lead Developer")
	Title string `yaml:"title" json:"title"`

	// Goal 角色目标
	Goal string `yaml:"goal" json:"goal"`

	// Backstory 背景故事，帮助 LLM 更好地扮演角色
	Backstory string `yaml:"backstory" json:"backstory"`

	// Expertise 专长领域
	Expertise []string `yaml:"expertise" json:"expertise"`

	// Tools 可用工具名称列表
	Tools []string `yaml:"tools" json:"tools"`

	// Personality 性格特点
	Personality string `yaml:"personality" json:"personality"`

	// Constraints 行为约束
	Constraints []string `yaml:"constraints" json:"constraints"`

	// AllowDelegation 是否允许委托任务给其他 Agent
	AllowDelegation bool `yaml:"allow_delegation" json:"allow_delegation"`

	// DelegateTo 可以委托给的 Agent 名称列表
	DelegateTo []string `yaml:"delegate_to" json:"delegate_to"`
}

// RoleBuilder 角色构建器
type RoleBuilder struct {
	role Role
}

// NewRole 创建角色构建器
func NewRole(name string) *RoleBuilder {
	return &RoleBuilder{
		role: Role{
			Name: name,
		},
	}
}

// Title 设置角色头衔
func (b *RoleBuilder) Title(title string) *RoleBuilder {
	b.role.Title = title
	return b
}

// Goal 设置角色目标
func (b *RoleBuilder) Goal(goal string) *RoleBuilder {
	b.role.Goal = goal
	return b
}

// Backstory 设置背景故事
func (b *RoleBuilder) Backstory(backstory string) *RoleBuilder {
	b.role.Backstory = backstory
	return b
}

// Expertise 设置专长领域
func (b *RoleBuilder) Expertise(areas ...string) *RoleBuilder {
	b.role.Expertise = areas
	return b
}

// Tools 设置可用工具
func (b *RoleBuilder) Tools(tools ...string) *RoleBuilder {
	b.role.Tools = tools
	return b
}

// Personality 设置性格特点
func (b *RoleBuilder) Personality(personality string) *RoleBuilder {
	b.role.Personality = personality
	return b
}

// Constraints 设置行为约束
func (b *RoleBuilder) Constraints(constraints ...string) *RoleBuilder {
	b.role.Constraints = constraints
	return b
}

// AllowDelegation 设置是否允许委托
func (b *RoleBuilder) AllowDelegation(allow bool) *RoleBuilder {
	b.role.AllowDelegation = allow
	return b
}

// DelegateTo 设置可委托的 Agent
func (b *RoleBuilder) DelegateTo(agents ...string) *RoleBuilder {
	b.role.DelegateTo = agents
	return b
}

// Build 构建角色
func (b *RoleBuilder) Build() Role {
	return b.role
}

// ToSystemPrompt 将角色转换为系统提示词
func (r Role) ToSystemPrompt() string {
	var prompt string

	if r.Title != "" {
		prompt += "You are a " + r.Title + ".\n\n"
	}

	if r.Goal != "" {
		prompt += "## Goal\n" + r.Goal + "\n\n"
	}

	if r.Backstory != "" {
		prompt += "## Background\n" + r.Backstory + "\n\n"
	}

	if len(r.Expertise) > 0 {
		prompt += "## Expertise\n"
		for _, e := range r.Expertise {
			prompt += "- " + e + "\n"
		}
		prompt += "\n"
	}

	if r.Personality != "" {
		prompt += "## Personality\n" + r.Personality + "\n\n"
	}

	if len(r.Constraints) > 0 {
		prompt += "## Constraints\n"
		for _, c := range r.Constraints {
			prompt += "- " + c + "\n"
		}
		prompt += "\n"
	}

	return prompt
}

// 预定义的常用角色

// ResearcherRole 研究员角色
var ResearcherRole = NewRole("researcher").
	Title("Senior Research Analyst").
	Goal("Uncover cutting-edge developments and provide insightful analysis").
	Backstory("You are an expert analyst with 10 years of experience in research. You have a keen eye for detail and are excellent at synthesizing complex information.").
	Expertise("research", "analysis", "data interpretation", "report writing").
	Personality("thorough, analytical, curious, objective").
	Constraints("Always cite sources", "Verify information from multiple sources").
	Build()

// WriterRole 作家角色
var WriterRole = NewRole("writer").
	Title("Content Writer").
	Goal("Create engaging and informative content").
	Backstory("You are a skilled writer with expertise in creating compelling narratives and clear explanations.").
	Expertise("writing", "editing", "storytelling", "content creation").
	Personality("creative, articulate, adaptable").
	Constraints("Maintain consistent tone", "Follow style guidelines").
	Build()

// DeveloperRole 开发者角色
var DeveloperRole = NewRole("developer").
	Title("Senior Software Developer").
	Goal("Design and implement high-quality software solutions").
	Backstory("You are an experienced developer with expertise in multiple programming languages and best practices.").
	Expertise("software development", "code review", "debugging", "system design").
	Personality("logical, detail-oriented, problem-solver").
	Constraints("Write clean, maintainable code", "Follow security best practices").
	Build()
