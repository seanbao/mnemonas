# WebDAV Mounting Guide

English | [简体中文](mounting-guide.md)

MnemoNAS exposes files through WebDAV. The Web UI provides management operations, while WebDAV provides desktop, mobile, and command-line access to the same storage.

## Connection Information

| Item | Value |
| --- | --- |
| Protocol | WebDAV over HTTP or HTTPS |
| Default URL | `http://<server-ip>:8080/dav` |
| Default local URL | `http://localhost:8080/dav` |
| Username | MnemoNAS username when `auth_type = "users"`; configured WebDAV username in `basic` mode |
| Password | MnemoNAS user password in `users` mode; configured or generated WebDAV password in `basic` mode |

For day-to-day mounting, `auth_type = "users"` is preferred.
It makes WebDAV follow MnemoNAS roles, groups, `home_dir`, directory access rules, home-scoped user quotas, and directory quotas.
The default legacy `basic` mode uses separate global WebDAV credentials.

Basic Auth credential handling:

- The running Web UI exposes the active WebDAV URL, Basic username, and readable generated password on the Settings -> WebDAV tab.
- Custom Basic passwords are not echoed back and should come from the config file or password manager.
- For Basic Auth with an auto-generated password, `<storage.root>/secrets.json` remains the server-side fallback.

See [configuration](configuration.en.md).

## macOS

### Finder

1. Open Finder.
2. Use **Go** -> **Connect to Server...** or press `Command+K`.
3. Enter `http://localhost:8080/dav`.
4. Click **Connect**.
5. Enter credentials for the selected auth mode: MnemoNAS username and password for `users`; WebDAV username and password for `basic`.

To disconnect, eject the mounted share from Finder's sidebar.

### Transmit

1. Create a new connection.
2. Select **WebDAV**.
3. Server: `localhost`.
4. Port: `8080`.
5. Path: `/dav`.
6. Connect with the credentials for the selected auth mode.

### Cyberduck

1. Create a new bookmark.
2. Select **WebDAV (HTTP)** or **WebDAV (HTTPS)**.
3. Server: `localhost:8080`.
4. Path: `/dav`.
5. Enter the credentials for the selected auth mode.

## Windows

### File Explorer

1. Open **This PC**.
2. Click **Map network drive**.
3. Pick a drive letter, such as `Z:`.
4. Folder: `http://localhost:8080/dav`.
5. Enable **Connect using different credentials**.
6. Finish and enter the credentials for the selected auth mode.

Windows' built-in WebDAV client has limited HTTP support. For non-HTTPS testing, run PowerShell as administrator:

```powershell
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" -Name "BasicAuthLevel" -Value 2
Restart-Service WebClient
```

For regular use, HTTPS through a reverse proxy is recommended.

### WinSCP

1. Install WinSCP.
2. Create a new site.
3. File protocol: **WebDAV**.
4. Host name: `localhost`.
5. Port: `8080`.
6. Directory: `/dav`.
7. Enter the credentials for the selected auth mode.

### Raidrive

1. Add a new NAS/WebDAV drive.
2. URL: `http://localhost:8080/dav`.
3. Choose a drive letter.
4. Connect with the credentials for the selected auth mode.

## Linux

### GNOME Files

1. Open Files.
2. Choose **Other Locations**.
3. Enter `dav://localhost:8080/dav`.
4. Connect.

### KDE Dolphin

Enter this address in the location bar:

```text
webdav://localhost:8080/dav
```

### davfs2

```bash
sudo apt install davfs2
sudo mkdir -p /mnt/nas
sudo mount -t davfs http://localhost:8080/dav /mnt/nas
sudo umount /mnt/nas
```

Optional `/etc/fstab` entry:

```fstab
http://localhost:8080/dav  /mnt/nas  davfs  _netdev,user,noauto  0  0
```

Credential file:

```text
http://localhost:8080/dav  <mnemonas-or-webdav-username>  <mnemonas-or-webdav-password>
```

### rclone

```bash
rclone config
```

Interactive values:

```text
n) New remote
name> mnemonas
Storage> webdav
url> http://localhost:8080/dav
vendor> other
user> <mnemonas-or-webdav-username>
pass> <mnemonas-or-webdav-password>
```

Mount:

```bash
rclone mount mnemonas: /mnt/nas --vfs-cache-mode full
```

Background mount:

```bash
rclone mount mnemonas: /mnt/nas --daemon --vfs-cache-mode full
```

## iOS and iPadOS

### Files

1. Open the Files app.
2. Tap the menu button.
3. Choose **Connect to Server**.
4. Enter `http://192.168.x.x:8080/dav`.
5. Enter the credentials for the selected auth mode.

### Documents by Readdle

1. Add a new connection.
2. Select WebDAV.
3. Enter the server URL and credentials for the selected auth mode.

## Android

### Solid Explorer

1. Add a cloud connection.
2. Select WebDAV.
3. Enter `http://192.168.x.x:8080/dav`.
4. Enter the credentials for the selected auth mode.

### Cx File Explorer

1. Open **Network**.
2. Add remote storage.
3. Select WebDAV and enter the server URL.
4. Enter the credentials for the selected auth mode.

### Total Commander

Install the WebDAV plugin, then add a WebDAV connection with the credentials for the selected auth mode.

## Troubleshooting

### Connection Refused

Check:

```bash
curl http://localhost:8080/health
```

If connecting from another device, use the server's LAN IP instead of `localhost`. Check the firewall and port mapping.

### Windows Cannot Connect over HTTP

Enable HTTP Basic Auth for the Windows WebClient service, or use HTTPS.

```powershell
Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters" -Name "BasicAuthLevel" -Value 2
Restart-Service WebClient
```

### Large Uploads Fail

For davfs2, increase cache settings in `/etc/davfs2/davfs2.conf`:

```text
cache_size  1024
buf_size    256
```

For rclone:

```bash
rclone copy localfile mnemonas:/ --size-only
```

Also check reverse proxy upload limits if using HTTPS.

### macOS Finder Feels Slow

Finder sends frequent `PROPFIND` requests. Try Transmit, Cyberduck, or rclone for heavier directory work.

### Lock Warnings

MnemoNAS implements virtual WebDAV locks for client compatibility.
If a client reports a lock issue, refresh the client, remount the share, and check whether another client is editing the same file.

## More Resources

- [WebDAV compatibility](webdav-compatibility.en.md)
- [Configuration reference](configuration.en.md)
- [FAQ](faq.en.md)
