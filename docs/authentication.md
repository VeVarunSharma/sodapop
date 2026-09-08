# Sodapop authentication

## Configure the application

The project owner maintains Sodapop's dedicated GitHub OAuth application with device authorization enabled. Keep the existing registration, public Client ID, and grants rather than creating a replacement application. Its public display name is **Sodapop** and its homepage is [https://sodapop.sh](https://sodapop.sh). A development build reads `SODAPOP_GITHUB_CLIENT_ID`; a distribution build may include the same public identifier at build time.

Use the [GitHub OAuth application settings](https://github.com/settings/developers) and [device-flow documentation](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps#device-flow). A distributed CLI must not contain a client secret or private key. Sodapop does not borrow the GitHub CLI or Copilot CLI's client identity, token, or login session.

For the existing application's badge, use [the Sodapop OAuth logo](../images/sodapop-oauth.png) and set **Badge background color** to `#09090B`. The square image includes room for GitHub's circular crop. Branding changes must preserve the existing Client ID, grants, callback URL, and device-flow setting; do not register a replacement application.

For local Make-based development, copy `.sodapop.env.example` to the ignored `.sodapop.env` and set only `SODAPOP_GITHUB_CLIENT_ID`. Direct script invocations and the installed executable do not load that file; export the variable explicitly or use a build with the public ID linked in. Release-candidate builds in [the Sodapop repository](https://github.com/VeVarunSharma/sodapop) read the same public ID from the Actions **variable** `SODAPOP_GITHUB_CLIENT_ID`. No token or client secret belongs in this setting.

Before release, prove that a user token issued to **this application** can list Copilot models and complete a real coding conversation under an eligible account and its organization policies. Generic GitHub sign-in is not proof of Copilot access. The [Copilot SDK OAuth guide](https://github.com/github/copilot-sdk/blob/v1.0.13/docs/setup/github-oauth.md) describes passing user tokens to the SDK.

## User flow

1. Start Sodapop and run `/login`.
2. Choose persistent or explicit session-only sign-in.
3. Sodapop requests device authorization and displays the verification code/link.
4. Authorize the Sodapop application in the browser; the terminal remains in Sodapop.
5. Sodapop obtains the user identity and connects to Copilot separately.
6. Choose an available model and submit a prompt.

Cancellation, denied authorization, expired device codes, unavailable network, and Copilot entitlement failures are distinct outcomes. Sodapop does not silently retry a coding action after authentication changes.

## Credential storage

Persistent credentials belong in the macOS Keychain, Linux Secret Service, or Windows Credential Manager. If the host has no usable secure store, choose session-only sign-in or cancel; Sodapop does not write token files into its preferences, state directory, project, or default Copilot configuration.

Session-only credentials last for the process. An expired or revoked token requires a secure supported refresh or a new device authorization. Sodapop does not introduce a backend or embed a secret to obtain refresh behavior.

Sign-out deletes Sodapop's credential and disconnects its engine, but preserves conversation history. Exiting normally does not sign out.

Never include tokens, client secrets, OAuth responses, or private device codes in issues, logs, screenshots, or test fixtures. Only the public Client ID belongs in `SODAPOP_GITHUB_CLIENT_ID`.

## Release qualification

The public client registration, minimum scopes, actual Copilot access, and native platform behavior must be qualified with owner-approved test identities. A missing client ID remains a visible blocker rather than a successful mock session. Routine CI uses injected HTTP and credential stores and never requires real account credentials.
