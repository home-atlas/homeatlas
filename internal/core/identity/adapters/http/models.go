package httpadapter

import (
	"net/http"

	"github.com/home-atlas/homeatlas/internal/core/identity/application"
	"github.com/home-atlas/homeatlas/internal/core/identity/domain"
)

type Client = application.Client
type Result = application.Result
type SetupInput = application.SetupInput
type LoginInput = application.LoginInput
type GrantInput = application.GrantInput
type AssistedInput = application.AssistedInput
type RecoverAccessInput = application.RecoverAccessInput
type VerifyEmailInput = application.VerifyEmailInput
type RequestRecoveryEmailInput = application.RequestRecoveryEmailInput
type UpdateHouseholdSettingsInput = application.UpdateHouseholdSettingsInput
type UpdateModuleInput = application.UpdateModuleInput
type InviteInput = application.InviteInput
type InspectInvitationInput = application.InspectInvitationInput
type AcceptInvitationInput = application.AcceptInvitationInput
type RenewAssistedEnrollmentInput = application.RenewAssistedEnrollmentInput
type InspectAssistedEnrollmentInput = application.InspectAssistedEnrollmentInput
type CompleteAssistedEnrollmentInput = application.CompleteAssistedEnrollmentInput
type PINLoginInput = application.PINLoginInput
type RegisterPINDeviceInput = application.RegisterPINDeviceInput
type UpdateUsernameInput = application.UpdateUsernameInput
type State = domain.State

func New(app *application.Application, publicURL string, modulesEnabled bool) (*Transport, error) {
	origin, err := application.ParseOrigin(publicURL)
	if err != nil {
		return nil, err
	}
	t := &Transport{Application: app, secure: origin.Scheme == "https", cookieName: "homeatlas_session", csrf: http.NewCrossOriginProtection(), host: origin.Host, modulesEnabled: modulesEnabled}
	if t.secure {
		t.cookieName = "__Host-homeatlas_session"
	}
	if err := t.csrf.AddTrustedOrigin(publicURL); err != nil {
		return nil, err
	}
	return t, nil
}
