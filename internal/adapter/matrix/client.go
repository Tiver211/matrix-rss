package matrix

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Credentials identify the bot account on the homeserver.
type Credentials struct {
	HomeserverURL string
	UserID        string
	Password      string
	DeviceName    string
	// PickleKey encrypts the end-to-end keys stored in the database.
	PickleKey string
}

// Database is the shared SQL connection used for the sync state and the end-to-end keys.
type Database struct {
	Conn    *sql.DB
	Dialect string
}

// connect logs in and prepares end-to-end encryption. The device is persisted in the database,
// so restarts reuse the same device and its keys.
func connect(ctx context.Context, creds Credentials, database Database, log zerolog.Logger) (*mautrix.Client, error) {
	client, err := mautrix.NewClient(creds.HomeserverURL, id.UserID(creds.UserID), "")
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	client.Log = log

	db, err := dbutil.NewWithDB(database.Conn, database.Dialect)
	if err != nil {
		return nil, fmt.Errorf("wrap database: %w", err)
	}
	helper, err := cryptohelper.NewCryptoHelper(client, []byte(creds.PickleKey), db)
	if err != nil {
		return nil, fmt.Errorf("create crypto helper: %w", err)
	}
	helper.LoginAs = &mautrix.ReqLogin{
		Type:                     mautrix.AuthTypePassword,
		Identifier:               mautrix.UserIdentifier{Type: mautrix.IdentifierTypeUser, User: creds.UserID},
		Password:                 creds.Password,
		InitialDeviceDisplayName: creds.DeviceName,
	}
	helper.DecryptErrorCallback = func(evt *event.Event, err error) {
		log.Warn().Err(err).Stringer("room_id", evt.RoomID).Stringer("event_id", evt.ID).Msg("cannot decrypt message")
	}
	if err := helper.Init(ctx); err != nil {
		return nil, fmt.Errorf("init encryption: %w", err)
	}
	client.Crypto = helper
	return client, nil
}
