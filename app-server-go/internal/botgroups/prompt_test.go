package botgroups

import (
	"strings"
	"testing"
)

func TestDeliveryPromptIdentifiesEachMemberOfTheSameAgent(t *testing.T) {
	alias := func(value string) *string { return &value }
	group := Group{
		ID:      "group-review",
		Name:    "API 授权复核",
		MaxHops: 10,
		Members: []Member{
			{ID: "design", AgentID: "codex", Alias: alias("方案设计"), Workspace: alias("/design"), SystemRolePrompt: alias("解释已经确认的设计")},
			{ID: "developer", AgentID: "codex", Alias: alias("OpsCore开发"), Workspace: alias("/development")},
		},
	}
	for _, member := range group.Members {
		t.Run(member.ID, func(t *testing.T) {
			prompt := deliveryPrompt(group, member, 4, nil)
			if !strings.Contains(prompt, "本次是本轮第 4 次接力，上限 10 次；本次回复后剩余 6 次。") {
				t.Fatalf("relay progress missing: %s", prompt)
			}
			if !strings.Contains(prompt, "你在本群的身份是 @"+memberName(member)+"。") {
				t.Fatalf("recipient identity missing: %s", prompt)
			}
			if strings.Count(prompt, "（你）") != 1 || !strings.Contains(prompt, "- @"+memberName(member)+"（你）") {
				t.Fatalf("recipient was not uniquely marked in roster: %s", prompt)
			}
			if !strings.Contains(prompt, "你的工作空间："+*member.Workspace) {
				t.Fatalf("recipient workspace missing: %s", prompt)
			}
			if member.ID == "design" && !strings.Contains(prompt, "你的群内职责：解释已经确认的设计") {
				t.Fatalf("recipient role missing: %s", prompt)
			}
			if member.ID == "developer" && strings.Contains(prompt, "你的群内职责：解释已经确认的设计") {
				t.Fatalf("another member's role was assigned to recipient: %s", prompt)
			}
		})
	}
}

func TestDeliveryPromptSeparatesRosterAndMessagesWithoutChangingMultilineText(t *testing.T) {
	member := Member{AgentID: "design"}
	group := Group{ID: "g", Name: "讨论", Members: []Member{member}, MaxHops: 20}
	userText := "@design 请先说明\n第一点：授权范围\n  第二点：复核条件\n\n```text\n保留代码\n  保留缩进\n```"
	prompt := deliveryPrompt(group, member, 20, []Message{
		{SenderName: "用户", Text: userText},
		{SenderName: "开发", Text: "等待方案说明\n然后确认理解"},
	})
	if !strings.Contains(prompt, "本次是本轮第 20 次接力，上限 20 次；本次回复后剩余 0 次。") {
		t.Fatalf("last relay progress missing: %s", prompt)
	}
	if !strings.Contains(prompt, "- @design（你）\n\n以下尚未发送给你的消息按群聊顺序排列：\n\n[用户]\n"+userText+"\n\n[开发]\n等待方案说明\n然后确认理解") {
		t.Fatalf("prompt structure or message line breaks changed: %s", prompt)
	}
	if !strings.Contains(prompt, "共享黑板 session_key=group::g") {
		t.Fatal("shared blackboard scope missing")
	}
	if strings.Contains(prompt, "你的工作空间：") || strings.Contains(prompt, "你的群内职责：") {
		t.Fatal("unspecified workspace or role was invented")
	}
}
