package app

func lastMessage(m *OS) string {
	if len(m.Notifications) == 0 {
		return ""
	}
	return m.Notifications[len(m.Notifications)-1].Message
}
