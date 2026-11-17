# Optional Tailscale lab

Use this integration when the server should be reachable only from your tailnet. Any compatible Debian/Ubuntu amd64 VM works; Proxmox is one possible hypervisor. No Proxmox API is used by the application or deploy pipeline.

1. Provision the VM and install the prerequisites from [systemd setup](systemd.md).
2. Join it to your tailnet. Enable Tailscale SSH and HTTPS as described in the [Tailscale documentation](https://tailscale.com/kb/1312/serve).
3. Install the service using its actual private HTTPS origin:

```sh
sudo bash deploy/systemd/install.sh https://VM.TAILNET.ts.net:8443 --with-deploy-user
sudo tailscale set --ssh
sudo tailscale serve --bg --https=8443 http://127.0.0.1:8090
```

4. Configure scoped tailnet policy: the CI identity `tag:homeatlas-ci` may SSH as `deploy` to the target `tag:homeatlas-lab` and reach its HTTPS port 8443. Grant the tag ownership needed by that identity. Preserve other deployment identities/rules. The application contains household-private data; grant CI access only to the required VM and ports.
5. Configure [Tailscale GitHub OIDC federation](https://github.com/tailscale/github-action) for this repository's `lab` environment. Match its actual subject and audience exactly; a repository using customized immutable-ID subjects must retain those values. No permanent Tailscale auth key is required in GitHub.
6. In the GitHub `lab` environment set `LAB_HOST` to the VM's Tailscale FQDN without a scheme/port, plus `TS_CLIENT_ID` and `TS_AUDIENCE`. Allow main without approval/wait requirements if unattended deploy is intended.
7. Set the **repository Actions variable** `LAB_DEPLOY_ENABLED=true`. Removing it or setting it to `false` skips deploy while preserving CI/releases.

The [reusable workflow](../../.github/workflows/deploy-lab.yml) downloads and verifies the just-published runtime assets, joins the tailnet ephemerally, streams them through Tailscale SSH to the fixed receiver and checks private HTTPS health. It does not open LAN/public SSH or publish the application to the internet. The exhaustive suite remains manual.

The lab workflow deliberately keeps the `.ts.net` and 8443 constraints for this recipe. Generic systemd installation accepts other HTTPS origins and can use any suitable reverse proxy. Use `https://VM.TAILNET.ts.net:8443` from an authorized device on the tailnet.
