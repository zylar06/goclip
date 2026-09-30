export type HighlightScenario = { id: string; label: string; description: string; rules: string }

export const highlightScenarios: HighlightScenario[] = [
  { id: 'general', label: '通用精彩片段', description: '适合大多数视频，寻找最值得继续观看的片段。', rules: '找出有明确事件、转折、结果或信息密度较高的片段；跳过寒暄、等待和重复内容。' },
  { id: 'football-goals', label: '足球进球与关键进攻', description: '进球、射门、扑救、关键传球和庆祝。', rules: '重点寻找进球、射门、门将扑救、关键传球、反击和进球庆祝；排除长时间控球、暂停和无关回放。' },
  { id: 'basketball', label: '篮球得分与关键回合', description: '得分、封盖、抢断和比赛转折。', rules: '重点寻找三分、扣篮、关键得分、封盖、抢断和最后回合；排除罚球准备、暂停和普通运球。' },
  { id: 'gameplay', label: '游戏击杀与胜利', description: '击杀、连招、胜利、失败和关键操作。', rules: '重点寻找可见的击杀、胜利、失败、关键操作和结果画面；排除菜单、加载和长时间移动。' },
  { id: 'teaching', label: '演讲与知识重点', description: '结论、方法、反差和可复述观点。', rules: '重点寻找完整结论、方法、数字、反差和可复述观点；保留必要的上下文，不截断理由和例子。' },
  { id: 'demo', label: '产品演示关键步骤', description: '操作前后变化、关键功能和结果。', rules: '重点寻找清晰的操作步骤、界面变化、功能演示和最终结果；排除等待加载、重复点击和无变化画面。' },
]
