# d2l-cli

A terminal client for D2L Brightspace, set up for the University of Waterloo's Learn (learn.uwaterloo.ca).

Run `d2l` to browse your courses and their content, download files into a local folder that mirrors the course structure, and open them in the app you choose. It also shows announcements, assignment due dates with submission status, and grades, and has fuzzy search. `d2l sync` downloads every file from your courses in one go.

## Install

Download a binary for your platform from the [releases page](https://github.com/BalajiLeninrajan/d2l-cli/releases), or build it with Go:

```sh
go install github.com/BalajiLeninrajan/d2l-cli/cmd/d2l@latest
```

Logging in needs a Chromium-based browser (Chrome, Chromium, Brave, Edge, Vivaldi) or a Firefox-based one (Firefox, Zen, LibreWolf, Floorp).

d2l is developed and tested on Linux. It builds for macOS and Windows, but neither is tested or supported.

## Logging in

```sh
d2l login
```

Learn has no API keys for students, so d2l uses a browser session. `d2l login` opens your default browser in a separate window with a profile of its own, on Learn's login page. Sign in through Waterloo SSO and Duo as usual. Once Learn accepts the session, d2l saves it and closes the window. It prints who you are logged in as.

d2l launches the browser with its automation port open and reads the cookies from that window, over the Chrome DevTools Protocol for Chromium-based browsers or WebDriver BiDi for Firefox-based ones. It never reads your normal browser profile, so your other tabs and logins stay out of it.

The default browser comes from `xdg-settings`. Flatpak and Snap installs work; their login profile lives inside the app's sandbox folder. If the default can't be driven (GNOME Web, for example), d2l uses the first supported browser it finds and says which. To pick one yourself, set `browser` in the config to a path or command.

The login profile stays between logins (`~/.local/state/d2l/browser/<name>`, or `~/.var/app/<id>/data/d2l-login` for Flatpak). If you picked "stay signed in" at SSO, later logins first try a hidden window and often finish without showing anything. Pass `-i` to skip that attempt and show the window right away.

The session is stored with mode 0600 in `~/.local/state/d2l/session.json`. Sessions expire. When one does, `d2l sync` tries the hidden renewal before giving up. If that doesn't work, or another command says the session expired, run `d2l login` again.

If the browser login does not work on your machine, for example over SSH with no display, copy the `d2lSessionVal` and `d2lSecureSessionVal` cookies from a logged-in browser tab and paste them in:

```sh
d2l login --cookie -
```

It asks for `d2lSessionVal=...; d2lSecureSessionVal=...` without echoing it. It also reads that line from a pipe. Passing the cookies as the flag value works too, but then they end up in your shell history.

`d2l logout` deletes the saved session. `d2l logout --all` also deletes the login browser profiles, so the next login starts from scratch.

## Usage

| Command | What it does |
| --- | --- |
| `d2l` | Open the TUI. |
| `d2l login [-i] [--cookie -]` | Log in and save the session. |
| `d2l logout [--all]` | Delete the session, and with `--all` the login browser profiles. |
| `d2l sync [--all] [-n] [FILTER...]` | Download course files. |
| `d2l config [--edit] [--path]` | Print the config path, creating the file if needed. `--edit` opens it in `$EDITOR`. |
| `d2l version` | Print the version. |
| `d2l help` | Print help. |

### Sync

`d2l sync` downloads every visible file from your current and pinned courses. Files that are already up to date are skipped, so running it again only fetches what changed. Files that are locked or outside their release dates on Learn are left out and counted as not available yet.

```sh
d2l sync              # current and pinned courses
d2l sync cs135 math   # only courses whose name or code contains "cs135" or "math"
d2l sync --all        # every course you are enrolled in, past terms included
d2l sync -n           # list what would be downloaded without fetching
```

It prints one line per downloaded file and a summary at the end. It exits with status 1 if anything failed.

When a file changes on Learn, sync replaces your copy. Edits you made to the local file, such as PDF annotations saved in place, are lost. Keep annotated copies under another name or outside the download folder.

## Keys

In the courses list:

| Key | Action |
| --- | --- |
| `enter` | Open the course |
| `f` | Search the content of the listed courses |
| `d` | Download the course's files |
| `w` | Open the course on Learn |
| `a` | Switch between current and all courses |
| `r` | Reload |
| `q` | Quit |

Inside a course, `1` to `4`, `tab` and `shift+tab` switch between the Content, Announcements, Assignments and Grades tabs. `f` searches the course, `r` reloads the tab, `esc` goes back and `q` closes the current screen.

| Tab | `enter` | Other keys |
| --- | --- | --- |
| Content | Open a file, link or folder | `d` downloads the file or folder, `w` opens it on Learn |
| Announcements | Read the announcement | In an announcement, `d` saves the attachments and `o` opens the first one |
| Assignments | Open the submission page | `w` does the same |
| Grades | Show the grader's comments | The final grade comes first once it is released |

Opening a file downloads it first if the local copy is missing or out of date. Pages that only work on Learn, such as quizzes, open in the web browser.

Quitting while downloads are running asks you to press `q` again. The downloads stop and their partial files are removed.

In search, type to narrow the results and press `enter` to keep the filter. Then `enter` opens a result, `d` downloads it and `w` opens it on Learn.

Press `/` in any list to filter it. `esc` clears the filter.

## Configuration

The config file lives at `~/.config/d2l/config.toml`. Run `d2l config` to create it with the defaults.

```toml
base_url = "https://learn.uwaterloo.ca"
download_dir = "~/Learn"
concurrency = 4

# Browser used by "d2l login", as a path or command. Leave unset to use the
# default browser. Chromium- and Firefox-based browsers work.
# browser = "/usr/bin/chromium-browser"

# Files open with xdg-open unless the extension is listed here.
# The file path is appended as the last argument.
[openers]
pdf = "zathura --fork"
pptx = "libreoffice"

# Openers that need the terminal. The TUI suspends while they run.
[terminal_openers]
md = "nvim"
txt = "less"
```

Opener commands run through `sh`, so they can include arguments.

## Where files go

Files go under `download_dir`, one folder per course, with a subfolder per module:

```
~/Learn/
  CS 135/
    Week 1/
      lec01.pdf
```

d2l sets each downloaded file's modification time to when the file last changed on Learn. That is how it tells whether a local copy is current.

When two files in one module have the same name, the older topic keeps the name and the others get their topic ID added, as in `handout (48213).pdf`. Folders with the same name get the same treatment.

Announcement attachments go in `Announcements/<title> (<id>)/` inside the course folder, one folder per announcement.

## How it works

d2l talks to the Brightspace Valence REST API using your session cookies, the same way the Learn website does. It asks the server which API versions it supports and uses the newest. If the server does not answer, it falls back to lp 1.49 and le 1.82.
