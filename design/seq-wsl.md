# Sequence: ark.exe called from WSL2
**Requirements:** R3494, R3495, R3496, R3497, R3499, R3500, R3505, R3506, R3507, R3512, R3514

## 1. A CLI call from a WSL shell

```
WSL shell        ark.exe (parent)                           ark.exe (child)        server
   |                  |                                          |                    |
1.1 ~/.ark/ark ARGS: shim reads /proc/mounts, sets ARK_WSL=1 + ARK_WSL_MOUNTS via WSLENV/u, execs ark.exe
   |----------------->|                                          |                    |
1.2                   wslModeOn: windows, no ARK_WSL_CHILD, ARK_WSL=1 or parent == wsl.exe
1.3                   parseWSLMounts(ARK_WSL_MOUNTS) (nil → /mnt/<letter> default)
1.4                   scrub env: unset ARK_WSL, ARK_WSL_MOUNTS; set ARK_WSL_CHILD=1
1.5                   translateWSLArgs: /mnt/c/x → C:\x; /home/... → error, exit 1
1.6                   exempt (luhmann attach, nano, serve, wsl-init)? → run in-process, unfiltered
1.7                   runWSLFiltered: exec self, stdin passed through
   |                  |----------------------------------------->|                    |
1.8                   |                                          normal CLI dispatch  |
   |                  |                                          |------------------->|
1.9                   wslFilter: C:\a → /mnt/c/a, "C:\\a" → "/mnt/c/a", hold split tails
   |<-----------------|<-----------------stdout/stderr-----------|                    |
1.10                  flush filters; exit with the child's code
```

## 2. Setting up the Linux side

```
WSL shell                          ark.exe wsl-init                       Windows ~/.ark
   |                                     |                                      |
2.1 eval "$(/mnt/c/Users/me/.ark/ark.exe wsl-init)"
   |------------------------------------>|                                      |
2.2                                      refuse unless GOOS == windows          |
2.3                                      ReadDir(arkDir); installed ark.exe; installLinkSet(arkDir)
   |                                     |------------------------------------->|
2.4                                      wslInitScript: winPathToLinux on every path
2.5                                      print the sh script (creates nothing)
   |<------------------------------------|                                      |
2.6 subshell: ~/.ark guard, write shim, ark_link each entry and each .claude link
```
