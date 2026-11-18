package application

type RecoverAccessInput struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

type VerifyEmailInput struct {
	Code string `json:"code"`
}

type RequestRecoveryEmailInput struct {
	Email string `json:"email"`
}

type UpdateHouseholdSettingsInput struct {
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

type UpdateModuleInput struct {
	Enabled *bool `json:"enabled"`
}

type InviteInput struct {
	Email       string   `json:"email"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	SendEmail   bool     `json:"sendEmail"`
}

type InspectInvitationInput struct {
	Token string `json:"token"`
}

type AcceptInvitationInput struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

type RenewAssistedEnrollmentInput struct {
	Password string `json:"password"`
}

type InspectAssistedEnrollmentInput struct {
	Token string `json:"token"`
}

type CompleteAssistedEnrollmentInput struct {
	Token      string `json:"token"`
	PIN        string `json:"pin"`
	DeviceName string `json:"deviceName"`
}

type PINLoginInput struct {
	PIN string `json:"pin"`
}

type RegisterPINDeviceInput struct {
	PIN        string `json:"pin"`
	Password   string `json:"password"`
	DeviceName string `json:"deviceName"`
}

type UpdateUsernameInput struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
