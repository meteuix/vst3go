English | [한국어](commercial-authentication.ko.md)

# Commercial Authentication And Licensing

This guide explains how to add login, license-key activation, and entitlement checks to a commercial plugin built on top of `vst3go`.

It is a product-design guide, not legal advice. It focuses on architecture, integration points, and operational tradeoffs.

## 1. What `vst3go` Should And Should Not Own

`vst3go` should own:

- the VST3 wrapper
- parameters and state plumbing
- editor snapshot plumbing
- bundle/build/validation scaffolding

Your downstream product repo should own:

- user accounts
- login UI
- license-key issuance
- activation/refresh networking
- entitlement policy
- payment integration
- customer support flows

Do not try to turn `vst3go` into an auth framework. Keep authentication adjacent to the plugin, not inside the runtime layer.

## 2. The Core Rule: Never Put Trust In The Plugin Binary Alone

Commercial plugin binaries are distributed to end users. That means:

- any secret embedded in the binary can eventually be extracted
- any local-only “license check” can be patched or bypassed
- any offline token can be copied if you do not bind it properly

So the only trustworthy source of truth should be a server-issued or server-signed entitlement model.

The plugin may cache a local proof of entitlement, but it should not be the authority.

## 3. Recommended Architecture

The most practical commercial architecture is:

1. user creates or signs into an account
2. user activates a license key or subscription entitlement
3. backend returns a signed entitlement object
4. plugin stores the entitlement locally
5. plugin periodically refreshes entitlement with the backend
6. plugin allows a grace period when the backend is temporarily unreachable
7. plugin disables premium behavior when entitlement expires or is revoked

That architecture works for:

- one-time perpetual licenses
- subscription licenses
- seat-based/team licenses
- time-limited trials
- offline activation with later refresh

## 4. Choose An Entitlement Model Early

Before you write code, choose the commercial model.

### Option A: License Key Only

User enters a key, backend validates it, backend returns activation.

Good for:

- simple checkout flows
- perpetual licenses
- small product catalogs

Tradeoffs:

- key sharing is easier
- support requests can increase if users lose keys
- account recovery is limited unless you also support login

### Option B: Account Login

User logs into an account and the app discovers the licenses attached to that account.

Good for:

- subscriptions
- multiple products
- seat management
- easy license recovery

Tradeoffs:

- you must build and maintain auth UX
- token refresh and session handling become part of the product

### Option C: Login + License Key

User logs in, then enters a key to attach it to the account.

This is often the best practical model because it supports:

- account recovery
- password reset
- device management
- key redemption
- subscription or perpetual licensing

For most commercial audio plugins, this is the recommended starting point.

## 5. Keep The Audio Thread Out Of It

The audio callback must not:

- perform network requests
- block on locks held by UI/network code
- wait for license server responses
- write large files
- do expensive crypto repeatedly

Instead:

- validate license state outside the audio thread
- cache the result in memory
- let `ProcessAudio` read a cheap atomic or lock-free flag

The audio thread should only ask:

- is this plugin currently licensed?
- is grace mode active?
- are premium features unlocked?

That check should be O(1) and non-blocking.

## 6. Put The Auth UI In The Editor Or A Separate Settings Surface

For a browser-rendered editor, the easiest place for auth UI is usually the editor shell itself or a settings panel inside the downstream app.

Common screens:

- sign in
- enter license key
- activate this device
- deactivate this device
- view entitlement status
- retry sync
- restore purchase

Good UX principle:

- keep the audio UI usable even if the license UI is hidden behind a settings route
- never force a user to visit a separate app unless you have a strong reason

## 7. Recommended Data Flow

The flow should look like this:

1. user enters email/password or license key
2. downstream app sends credentials to backend over TLS
3. backend verifies identity and license ownership
4. backend returns an entitlement blob
5. downstream app stores the entitlement locally
6. plugin reads the local entitlement on startup
7. plugin refreshes the entitlement in the background

The plugin binary itself should not be the thing that “knows” the secret.

## 8. Suggested Backend Endpoints

A small commercial backend often needs endpoints like:

- `POST /auth/login`
- `POST /auth/logout`
- `POST /licenses/activate`
- `POST /licenses/deactivate`
- `POST /licenses/refresh`
- `GET /licenses/me`
- `POST /licenses/redeem`
- `POST /licenses/trial/start`

You can keep the exact shape different, but these are the common operations you will need.

## 9. What The Plugin Should Store Locally

Store only what you need to support offline startup and fast reload.

Typical cached fields:

- account ID
- entitlement ID
- product ID
- license tier
- issue time
- expiry time
- refresh deadline
- device binding identifier
- signature or verification data

Do not store raw passwords.
Do not store server secrets.

## 10. Use Signed Entitlements

The safest common pattern is:

- backend issues an entitlement object
- backend signs the object
- plugin verifies the signature locally
- backend can revoke or refresh the entitlement later

This lets the plugin make a local decision quickly without trusting a mutable plain-text flag.

Typical entitlement contents:

- subject/user ID
- product ID
- license type
- seats
- device ID
- issue timestamp
- expiry timestamp
- grace period
- feature flags

Signature options:

- Ed25519
- ECDSA
- RSA

For a plugin product, Ed25519 is often a good fit because verification is fast and the public key can be embedded without creating a server secret.

## 11. Device Binding

If you want to limit simultaneous activations, bind the license to a device fingerprint.

Good signals:

- machine ID
- OS install identifier
- generated installation UUID
- account-managed device slot

Avoid relying on a single hardware fingerprint, because hardware can change and privacy policies differ by platform.

Recommended approach:

- generate an installation UUID on first run
- store it locally
- let the backend associate that install UUID with the account/license
- allow reactivation if the user moves machines

That is usually less brittle than trying to fingerprint CPU, disk, and motherboard values.

## 12. Offline Grace Periods

Commercial plugins need a sensible failure mode when the network is unavailable.

Recommended policy:

- allow a short grace period when entitlement refresh fails
- surface the exact reason in the UI
- keep the plugin usable during grace if the prior entitlement was valid
- eventually require revalidation if the license cannot be refreshed

This helps users on stage, on planes, or behind strict firewall policies.

## 13. What To Put In `vst3go` State Versus Product State

Keep this split clean:

- `vst3go` state: plugin parameters and plugin-owned custom state
- product state: entitlement cache, auth session, user profile, license history

In practice, the commercial auth state often belongs outside the VST3 state stream.

Why:

- license/session state may need independent refresh cycles
- plugin state should remain restorable even if auth is unavailable
- changing auth policy should not corrupt the user’s sound settings

The safe default is:

- save audio/plugin state in the VST3 state path
- save auth state in a separate product-managed store

## 14. Where To Store Auth State

Choose one of these:

- app data directory
- user config directory
- encrypted local store
- platform keychain or credential store

For sensitive refresh tokens, prefer platform credential storage when possible.

For non-sensitive entitlement metadata, a local config file is acceptable if it is signed and verified.

## 15. Suggested Storage Split

### Store In Keychain / Credential Manager

- refresh tokens
- long-lived session secrets
- device secrets if you truly need them

### Store In Normal Config

- entitlement cache
- verification metadata
- feature flags
- last sync time

## 16. Using `vst3go` With A Commercial Editor

If your plugin uses the browser-rendered editor:

- show login state in the editor
- show clear entitlement status
- let the user activate/deactivate a device
- keep auth UI separate from audio controls
- do not block the editor while waiting for the server

Good editor states:

- `Signed out`
- `Signed in`
- `Activated`
- `Grace period`
- `Expired`
- `Offline, last verified on ...`

The plugin should still load and present its audio state even when the auth panel is not available.

## 17. Recommended Runtime Checks

At runtime, the plugin should only need a simple entitlement state machine:

- `Unknown`
- `Valid`
- `Grace`
- `Expired`
- `Revoked`
- `SignInRequired`

The audio engine can read this state and decide whether premium features are enabled.

Suggested behavior:

- `Valid`: full features
- `Grace`: full features, warning banner
- `Expired`: limited features or mute, depending on your product policy
- `Revoked`: disable premium behavior immediately

Be careful with hard-mute policies; they can create support pain if they are too aggressive.

## 18. Trials

If you want trials, treat them as another entitlement type.

Common trial policies:

- time-limited trial from first activation
- time-limited trial from installation
- trial that requires account signup
- trial that can be upgraded to paid entitlement

Trials should still be signed and refreshable.

Do not rely only on local clock checks; always pair them with a server-issued record if you want them to be meaningful.

## 19. Offline Activation

If your users work on isolated studio machines, offer an offline activation path.

Typical flow:

1. plugin shows machine/installation ID
2. user copies that ID into a web portal or support form
3. backend generates an offline activation file
4. user imports the file into the plugin/app
5. plugin verifies signature and stores the entitlement

This is common in audio software because studio machines are not always online.

## 20. Logging And Privacy

Auth failures should be logged carefully.

Log:

- status codes
- retry deadlines
- entitlement state transitions
- signature verification failures

Do not log:

- passwords
- raw tokens
- full license keys
- private user data

If you need telemetry, make it opt-in and disclose it clearly.

## 21. Security Rules You Should Follow

- use TLS everywhere
- verify server certificates
- sign entitlement payloads
- keep secrets out of the plugin binary
- keep audio thread checks cheap
- assume local storage can be inspected
- assume network requests can fail
- assume any client-side check can be reverse engineered

Those are the realities of commercial plugin distribution.

## 22. A Practical `vst3go` Integration Pattern

In a downstream repo, your auth layer usually sits next to the plugin code, not inside `vst3go`.

Good split:

- `internal/auth`: login, activation, entitlement refresh
- `internal/license`: signature verification, entitlement cache
- `plugin`: processor and VST3 integration
- `web`: editor UI
- `cmd`: optional helper app or activation tool

Typical call flow:

1. the app starts
2. auth layer loads cached entitlement
3. plugin starts with cached state
4. auth layer refreshes in the background
5. plugin is notified if entitlement state changes

## 23. Example: How The Processor Might See License State

The processor should receive a cheap, cached view of entitlement state.

Example shape:

```go
type LicenseState struct {
	Status      string
	GraceExpiry int64
	FeatureMask uint32
}

type Processor struct {
	params       *param.Registry
	buses        *bus.Configuration
	licenseState atomic.Value
}

func (p *Processor) IsPremiumEnabled() bool {
	state, _ := p.licenseState.Load().(LicenseState)
	return state.Status == "Valid" || state.Status == "Grace"
}
```

The important point is not the exact type. The important point is that the realtime path reads a cached state, not a remote service.

## 24. Example: Where To Store Auth Metadata In The Editor

The editor can show:

- account email
- activation status
- device count
- last sync time
- expiry date
- upgrade link

But the editor should not be responsible for deriving entitlement truth on its own.

The editor is just the front door.

## 25. Suggested User Experience

A good commercial plugin UX usually looks like this:

- plugin loads
- if entitlement is valid, the audio UI is available immediately
- if the user is signed out, an auth panel is visible but the shell still loads
- if the license is expired, the UI explains what is missing and how to fix it
- if offline, the UI shows when the license was last verified

Keep the messaging direct and calm. Users should understand what is happening without reading a help page.

## 26. What Not To Do

- do not hide auth failures behind generic errors
- do not put secrets in `pkg/plugin`
- do not make `ProcessAudio` wait for server responses
- do not depend on local wall-clock checks alone
- do not store raw passwords on disk
- do not make the plugin unusable just because the editor is offline

## 27. Commercial Packaging Checklist

Before shipping a commercial plugin built on `vst3go`, make sure:

- licensing policy is documented
- offline behavior is defined
- grace period behavior is defined
- revocation behavior is defined
- support workflow exists for lost access
- entitlement recovery works
- updater/installer behavior is tested
- host validation still passes

## 28. Recommended Starting Point

If you are starting from scratch, the safest order is:

1. build the plugin without auth
2. add a backend and account login
3. add license activation
4. add signed entitlement cache
5. add offline grace
6. add device management
7. add revocation handling
8. add trial/up-sell UI

That order keeps the system understandable and debuggable.

## 29. Final Recommendation

For most commercial `vst3go` products, the best balance is:

- account login
- license key redemption
- signed entitlement cache
- offline grace
- background refresh
- separate product auth state from VST3 plugin state

That gives you a commercial workflow that is practical, supportable, and much harder to break than a pure client-side license check.
