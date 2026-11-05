package messenger

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-meta/pkg/messagix"
	"go.mau.fi/mautrix-meta/pkg/messagix/cookies"
	"go.mau.fi/mautrix-meta/pkg/messagix/httpclient"
	"go.mau.fi/mautrix-meta/pkg/messagix/types"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/ralph/tuon/internal/auth"
)

// ErrLoggedOut means Messenger no longer accepts the saved login.
var ErrLoggedOut = errors.New("Messenger logged you out")

// Field is one thing the login asks for (email, password, a code).
type Field struct {
	ID, Name, Description string
	Secret                bool // hide what's typed
	Optional              bool
}

// PasswordLogin logs in with email/phone and password the way the
// Messenger app does, asking for each step (including 2-factor codes)
// through ask. The password goes only to Meta; what comes back is a
// session, which the caller saves.
func PasswordLogin(ctx context.Context, log zerolog.Logger, ask func(Field) (string, error), say func(string)) (auth.Creds, error) {
	cli := messagix.NewClient(&cookies.Cookies{Platform: types.MessengerLiteIOS}, log, &messagix.Config{})
	input := map[string]string{}
	for i := 0; i < 25; i++ {
		step, ck, err := cli.MessengerLite.DoLoginSteps(ctx, input)
		if err != nil {
			return auth.Creds{}, friendlyLoginError(err)
		}
		if ck != nil {
			vals := map[string]string{}
			for k, v := range ck.GetAll() {
				vals[string(k)] = v
			}
			return auth.Creds{Platform: "messenger-lite", Cookies: vals}, nil
		}
		input = map[string]string{}
		if step == nil {
			continue
		}
		if step.Instructions != "" {
			say(step.Instructions)
		}
		if step.Type == bridgev2.LoginStepTypeDisplayAndWait || step.UserInputParams == nil {
			continue // e.g. "approve this login on your phone": ask again
		}
		for _, f := range step.UserInputParams.Fields {
			v, err := ask(Field{ID: f.ID, Name: f.Name, Description: f.Description,
				Secret: f.Type == bridgev2.LoginInputFieldTypePassword, Optional: f.Optional})
			if err != nil {
				return auth.Creds{}, err
			}
			input[f.ID] = v
		}
	}
	return auth.Creds{}, errors.New("login took too many steps; try again")
}

func friendlyLoginError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(strings.ToLower(msg), "password"):
		return fmt.Errorf("Meta didn't accept that login: %s", msg)
	case errors.Is(err, httpclient.ErrChallengeRequired):
		return errors.New("Meta wants you to confirm this login first. Open the Messenger app or messenger.com, approve it, then try again")
	}
	return err
}

// isLoggedOut: errors that mean the saved session is dead.
func isLoggedOut(err error) bool {
	return errors.Is(err, httpclient.ErrTokenInvalidated) || errors.Is(err, httpclient.ErrUserIDIsZero) ||
		errors.Is(err, httpclient.ErrChallengeRequired)
}
