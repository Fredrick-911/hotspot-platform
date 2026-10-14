# Setup Guide 1 — Run FreeRADIUS on Windows with WSL

This guide takes you from a Windows PC to a working FreeRADIUS server that:

- accepts a login and sends back a **speed limit** and **time limit**,
- rejects a wrong password,
- saves **usage records** (accounting) in PostgreSQL.

That is exactly what a MikroTik router will ask it to do in Phase 0.

**Time needed:** about 30 minutes.

---

## Contents

1. [What you are about to build](#1-what-you-are-about-to-build)
2. [Prepare Windows and WSL](#2-prepare-windows-and-wsl)
3. [Get the repository into WSL](#3-get-the-repository-into-wsl)
4. [Install and test FreeRADIUS](#4-install-and-test-freeradius)
5. [Everyday use](#5-everyday-use)
6. [How the pieces fit together](#6-how-the-pieces-fit-together)
7. [Let the MikroTik reach FreeRADIUS](#7-let-the-mikrotik-reach-freeradius)
8. [Troubleshooting](#8-troubleshooting)

---

## 1. What you are about to build

```
 Windows PC
 └── WSL (Ubuntu Linux)
      ├── PostgreSQL      ← stores users, plans and usage
      │     ▲
      │     │ reads / writes
      │     ▼
      └── FreeRADIUS      ← answers "is this user allowed?" on UDP 1812
                             records usage on UDP 1813
                                ▲
                                │ (Section 7, later)
                          MikroTik router
```

**Words you will see:**

| Word | Meaning |
|---|---|
| **WSL** | Windows Subsystem for Linux — a real Ubuntu Linux running inside Windows. |
| **RADIUS** | The standard language routers use to ask a server "can this user log in, and with what limits?" |
| **FreeRADIUS** | The free, widely used RADIUS server program. |
| **Client** (in RADIUS) | The **router** asking questions — not the person on WiFi. |
| **Shared secret** | A password the router and FreeRADIUS both know, so FreeRADIUS trusts the router. |
| **Access-Accept / Access-Reject** | FreeRADIUS's answers: "let them in" / "keep them out". |
| **Accounting** | The router's reports of how long a user was online and how much data they used. |
| **Port 1812 / 1813** | The network "doors" for logins (1812) and accounting (1813). They use **UDP**. |

---

## 2. Prepare Windows and WSL

### 2.1 Check WSL is version 2 with Ubuntu

Open **PowerShell** (Start menu → type "PowerShell") and run:

```powershell
wsl -l -v
```

You should see something like:

```
  NAME            STATE           VERSION
* Ubuntu-24.04    Running         2
```

- **VERSION must be 2.** If it shows 1: `wsl --set-version Ubuntu-24.04 2`
- **No Ubuntu listed?** Install it: `wsl --install -d Ubuntu-24.04`, restart, then open "Ubuntu" from the Start menu and create a username and password.
- Keep WSL updated: `wsl --update`

> Ubuntu 22.04 also works. Our CI tests on 24.04, so prefer that.

### 2.2 Open the Ubuntu terminal

Start menu → **Ubuntu**. Every command from here on runs in this terminal unless it says PowerShell.

Update the system once:

```bash
sudo apt update && sudo apt upgrade -y
sudo apt install -y git make unzip
```

`sudo` runs a command as administrator. It asks for the WSL password you created.

### 2.3 Set up Git

```bash
git config --global user.name  "Your Name"
git config --global user.email "you@example.com"
git config --global init.defaultBranch main
git config --global core.autocrlf input
```

The last line stops Git from converting files to Windows line endings, which would break the scripts.

### 2.4 Connect Git to GitHub with an SSH key

```bash
ssh-keygen -t ed25519 -C "you@example.com"   # press Enter 3 times to accept defaults
cat ~/.ssh/id_ed25519.pub                      # copy the line it prints
```

On GitHub: **Settings → SSH and GPG keys → New SSH key** → paste → save. Then test:

```bash
ssh -T git@github.com    # type "yes" the first time; expect "Hi <username>!"
```

### 2.5 Install VS Code's WSL extension (recommended)

1. Install [VS Code](https://code.visualstudio.com/) on **Windows**.
2. In VS Code, install the extension **"WSL"** (by Microsoft).
3. From the Ubuntu terminal, inside a project folder, type `code .` — VS Code opens connected to Linux.

### 2.6 Install Go inside WSL

```bash
cd /tmp
wget https://go.dev/dl/go1.24.7.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.24.7.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc
go version     # expect: go version go1.24.x linux/amd64
```

Any Go 1.24 or newer works; check [go.dev/dl](https://go.dev/dl/) for the latest file name.

---

## 3. Get the repository into WSL

> **Important:** keep the project inside your **Linux home folder** (`~/projects/...`), **not** under `/mnt/c/...`.
> Files under `/mnt/c` are the Windows drive: much slower, and file permissions break the scripts.

### 3A. If you are creating the repository (first time only)

1. On GitHub, click **New repository** → name it `hotspot-platform` → **Private** → do **not** add a README → **Create**.

2. Copy the starter zip from your Windows Downloads folder into WSL and unpack it:

   ```bash
   mkdir -p ~/projects && cd ~/projects
   cp /mnt/c/Users/<YourWindowsName>/Downloads/hotspot-platform.zip .
   unzip hotspot-platform.zip
   cd hotspot-platform
   ```

3. Replace the placeholder username with your GitHub username (or organisation) everywhere:

   ```bash
   grep -rl "Fredrick-911" . | xargs sed -i 's/Fredrick-911/your-real-username/g'
   ```

4. Make the scripts runnable, then check the Go code builds:

   ```bash
   chmod +x scripts/wsl/*.sh
   make check
   ```

5. Push it to GitHub:

   ```bash
   git init
   git add .
   git commit -m "Initial project structure and FreeRADIUS setup"
   git remote add origin git@github.com:your-real-username/hotspot-platform.git
   git push -u origin main
   ```

6. On GitHub, open the **Actions** tab. The CI pipeline runs automatically. All three jobs should turn green within a few minutes.

7. Protect `main` (see [CONTRIBUTING.md](../../CONTRIBUTING.md#protecting-main)).

### 3B. If you are a collaborator

```bash
mkdir -p ~/projects && cd ~/projects
git clone git@github.com:<owner>/hotspot-platform.git
cd hotspot-platform
```

---

## 4. Install and test FreeRADIUS

From inside `~/projects/hotspot-platform`, run **one command**:

```bash
make radius-setup
```

It runs four scripts in order. Here is what each one does.

### Step 1 — `scripts/wsl/01-install.sh`: install software

Installs from Ubuntu's package store:

| Package | Purpose |
|---|---|
| `freeradius` | The RADIUS server |
| `freeradius-utils` | Test tools: `radtest`, `radclient` |
| `freeradius-postgresql` | Lets FreeRADIUS read users from PostgreSQL |
| `postgresql` | The database |
| `gettext-base` | `envsubst`, used to fill in our config templates |

### Step 2 — `scripts/wsl/02-setup-database.sh`: create the database

1. Creates `.env` from `.env.example` and fills it with **random secrets**. `.env` is git-ignored, so secrets never reach GitHub.
2. Creates a PostgreSQL user `radius` and a database `radius`.
3. Loads FreeRADIUS's official tables (`radcheck`, `radreply`, `radacct`, ...).
4. Adds test plans and users from `radius/sql/seed-dev.sql`:

| Username | Password | Plan | Limits |
|---|---|---|---|
| `test-hour` | `pass1234` | `plan-1hour` | 1 hour, 2 Mbps up / 5 Mbps down |
| `test-data` | `pass1234` | `plan-500mb` | 500 MB total, 3M / 10M |
| `test-custom` | `pass1234` | none | 2 minutes, 1M / 1M (set on the user directly) |

### Step 3 — `scripts/wsl/03-configure-freeradius.sh`: configure FreeRADIUS

1. Fills in `radius/clients.conf.tmpl` and `radius/mods-available/sql.tmpl` with values from `.env` and installs them in `/etc/freeradius/3.0/`. The original files are kept as `*.orig`.
2. Enables the SQL module, so FreeRADIUS looks users up in PostgreSQL.
3. If IPv6 is switched off in your WSL, disables FreeRADIUS's IPv6 listeners (otherwise it won't start).
4. Checks the configuration (`freeradius -C`) and restarts FreeRADIUS.

### Step 4 — `scripts/wsl/04-test-radius.sh`: prove it works

Acts like a router and checks three things. The end of the output should be:

```
  PASS: login accepted (Access-Accept)
  PASS: speed limit 2M/5M sent to router
  PASS: 1-hour time limit sent to router
  PASS: wrong password rejected (Access-Reject)
  PASS: session saved in radacct: user=test-hour, 60s, 1 MB up, 5 MB down

All RADIUS tests passed. FreeRADIUS is ready for the MikroTik.
```

✅ **If you see that, FreeRADIUS is working.** If not, see [Troubleshooting](#8-troubleshooting).

---

## 5. Everyday use

Run `make` on its own to see every command.

### After restarting Windows or WSL

WSL may not restart services automatically. Start them with:

```bash
make radius-start
```

### Watch FreeRADIUS think (debug mode)

This is the single most useful tool. It shows every request and every decision.

```bash
make radius-debug      # leave this running
```

In a **second** Ubuntu terminal:

```bash
radtest test-hour pass1234 127.0.0.1 0 testing123
```

The first terminal prints the whole conversation. Look for `Sending Access-Accept`. Press **Ctrl+C** to stop debug mode, then `make radius-start` to run it in the background again.

### Look inside the database

```bash
make db-shell
```

Useful queries (type `\q` to exit):

```sql
-- Who can log in, and with what password
SELECT username, attribute, value FROM radcheck;

-- Which user is on which plan
SELECT * FROM radusergroup;

-- What each plan gives
SELECT groupname, attribute, value FROM radgroupreply ORDER BY groupname;

-- Recent sessions and data used
SELECT username, acctstarttime, acctsessiontime, acctinputoctets, acctoutputoctets
FROM radacct ORDER BY radacctid DESC LIMIT 10;
```

### Add a user by hand (this is what the Go backend will do automatically)

```sql
INSERT INTO radcheck (username, attribute, op, value)
VALUES ('voucher-ABC123', 'Cleartext-Password', ':=', 'ABC123');

INSERT INTO radusergroup (username, groupname, priority)
VALUES ('voucher-ABC123', 'plan-1hour', 1);
```

Test it with no restart needed, because FreeRADIUS reads the database on every login:

```bash
radtest voucher-ABC123 ABC123 127.0.0.1 0 testing123
```

### Changed a template or `.env`?

```bash
make radius-config && make radius-test
```

---

## 6. How the pieces fit together

### What happens during one login

```
Router ──Access-Request (user, password)──► FreeRADIUS
                                              │
                                              ├─ 1. Is this router in clients.conf, with the right secret?
                                              │     no → silently ignore
                                              ├─ 2. Look up user in radcheck   (password)
                                              ├─ 3. Look up radusergroup → radgroupreply (plan limits)
                                              │     and radreply                (user-specific limits)
                                              ├─ 4. Password matches?
                                              │     no → Access-Reject
Router ◄──Access-Accept + limits────────────┘ yes
         e.g. Mikrotik-Rate-Limit = "2M/5M"
              Session-Timeout     = 3600
```

Then, while the user is online:

```
Router ──Accounting Start / Interim-Update / Stop──► FreeRADIUS ──► radacct table
```

### The reply attributes that control a MikroTik user

| Attribute | Example | Effect on the MikroTik |
|---|---|---|
| `Mikrotik-Rate-Limit` | `2M/5M` | Speed cap: **upload/download** from the user's view |
| `Session-Timeout` | `3600` | Disconnect after this many **seconds** |
| `Mikrotik-Total-Limit` | `524288000` | Disconnect after this many **bytes** (up + down) |
| `Acct-Interim-Interval` | `60` | Send a usage update every 60 seconds |
| `Idle-Timeout` | `300` | Disconnect after 5 minutes of no traffic |

### Where files live

| In this repo (edit these) | Installed to (don't edit by hand) |
|---|---|
| `radius/clients.conf.tmpl` | `/etc/freeradius/3.0/clients.conf` |
| `radius/mods-available/sql.tmpl` | `/etc/freeradius/3.0/mods-available/sql` |
| `radius/sql/seed-dev.sql` | rows in the `radius` database |
| `.env` (secrets, never committed) | values substituted into the above |

The rule: **change files in the repo, then run `make radius-config`**. Hand edits in `/etc/freeradius` get overwritten and aren't shared with the team.

---

## 7. Let the MikroTik reach FreeRADIUS

*You only need this when the MikroTik arrives.*

### The problem

WSL normally sits behind a private network inside Windows. Devices on your WiFi (like the MikroTik) can't see it. Windows' usual workaround (`netsh portproxy`) only forwards **TCP**, but RADIUS uses **UDP**.

### The fix: WSL "mirrored" networking (Windows 11 22H2 or newer)

Mirrored mode makes WSL share your PC's real network address.

**1.** In Windows, open Notepad and save this as `C:\Users\<YourWindowsName>\.wslconfig` (choose "All files" so it isn't saved as `.txt`):

```ini
[wsl2]
networkingMode=mirrored
```

**2.** In **PowerShell**, restart WSL:

```powershell
wsl --shutdown
```

Then reopen Ubuntu and run `make radius-start`.

**3.** In **PowerShell as Administrator** (right-click → Run as administrator), open the RADIUS ports in both Windows firewalls:

```powershell
# Windows Defender Firewall
New-NetFirewallRule -DisplayName "FreeRADIUS (WSL)" -Direction Inbound -Protocol UDP -LocalPort 1812,1813 -Action Allow

# Hyper-V firewall (protects WSL in mirrored mode)
New-NetFirewallHyperVRule -Name "FreeRADIUS-WSL" -DisplayName "FreeRADIUS (WSL)" -Direction Inbound -VMCreatorId '{40E0AC32-46A5-438A-A0B2-2B479E8F2E90}' -Protocol UDP -LocalPorts 1812,1813
```

**4.** Connect your laptop to the MikroTik (WiFi or cable). In PowerShell, run `ipconfig` and note the laptop's IPv4 address on that adapter, e.g. `192.168.88.254`. Make sure Windows treats that network as **Private**.

**5.** Check `MIKROTIK_LAB_SUBNET` in `.env` matches the MikroTik's network (default `192.168.88.0/24`). If you change it, run `make radius-config`.

**6.** Preview of the MikroTik side (the full router setup is the next guide). Use your laptop IP and the `MIKROTIK_LAB_SECRET` value from `.env`:

```
/radius add service=hotspot address=192.168.88.254 secret=<MIKROTIK_LAB_SECRET>
/radius incoming set accept=yes
/ip hotspot profile set [find] use-radius=yes
```

**On Windows 10?** Mirrored mode isn't available. Use a small cloud VPS (Ubuntu 24.04) and run the same `make radius-setup` there. The scripts work unchanged.

---

## 8. Troubleshooting

**First step for almost every problem:** run `make radius-debug` in one terminal, repeat the failing action in another, and read the output.

| Symptom | Cause | Fix |
|---|---|---|
| `/usr/bin/env: 'bash\r': No such file or directory` | Files got Windows line endings | Clone inside WSL (not `/mnt/c`), and set `git config --global core.autocrlf input`. Then `git rm --cached -r . && git reset --hard` |
| `Permission denied` running a script | Scripts not executable | `chmod +x scripts/wsl/*.sh` |
| `No reply from server` in `radtest` | FreeRADIUS not running | `make radius-start`, then `make radius-status` |
| Debug mode: `Failed binding to ... port 1812: Address already in use` | Background FreeRADIUS still running | `make radius-stop`, then `make radius-debug` |
| Debug mode: `Address family not supported by protocol` | IPv6 off in WSL | `make radius-config` (handles it automatically) |
| Debug mode: `Ignoring request ... from unknown client` | Router's IP isn't in `clients.conf` | Fix `MIKROTIK_LAB_SUBNET` in `.env`, then `make radius-config` |
| Debug mode: `Shared secret is incorrect` | Router and `.env` secrets differ | Copy `MIKROTIK_LAB_SECRET` from `.env` exactly into the router |
| Debug mode: `rlm_sql ... failed to connect` | PostgreSQL stopped or wrong password | `make radius-start`. If you changed the DB password in `.env`, re-run `make radius-setup` |
| Test 1 passes but limits are missing | User not in a plan group | Check `radusergroup` and `radgroupreply` in `make db-shell` |
| MikroTik gets no answer, `radtest` works | Network/firewall | Redo Section 7 steps 1–4. Confirm the laptop IP in the MikroTik's `/radius` entry |

### Undo everything

The scripts saved the original FreeRADIUS files with a `.orig` ending:

```bash
cd /etc/freeradius/3.0
for f in clients.conf mods-available/sql sites-available/default; do
  [ -f "$f.orig" ] && sudo mv "$f.orig" "$f"
done
sudo rm -f mods-enabled/sql
sudo service freeradius restart
```
