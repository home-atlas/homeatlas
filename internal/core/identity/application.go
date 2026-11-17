package identity

type Client struct {
	SessionToken, DeviceToken, RemoteAddress string
	HasSession, HasDevice                    bool
}
type Result struct {
	Data                      any
	SessionToken, DeviceToken string
	EndSession                bool
}
