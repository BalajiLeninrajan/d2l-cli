# d2l-cli

A terminal client for D2L Brightspace, set up for the University of Waterloo's Learn (learn.uwaterloo.ca).

Run `d2l` to browse your courses and their content, download files into a local folder that mirrors the course structure, and open them in the app you choose. It also shows announcements, assignment due dates with submission status, and grades, and has fuzzy search. `d2l sync` downloads every file from your courses in one go.

## Install

```sh
go install github.com/BalajiLeninrajan/d2l-cli/cmd/d2l@latest
```

Logging in works best with a Firefox-based default browser (Firefox, Zen, LibreWolf, Floorp). Otherwise it needs a Chromium-family browser such as Chromium, Chrome, Brave or Edge.

## Logging in

```sh
d2l login
```

Learn has no API keys for students, so d2l uses your browser session.

By default `d2l login` uses your default browser. If you are already logged in to Learn there, it picks up that session right away. If not, it opens Learn in a new tab. Sign in through Waterloo SSO and Duo as usual. d2l then waits for the browser to save the session. Firefox-based browsers save it about every 15 seconds, so expect a short pause. d2l checks the session against the API and prints who you are logged in as.

This works because Learn's session cookies only live in memory, and Firefox-based browsers write them to the profile's session store (`sessionstore-backups/recovery.jsonlz4`). d2l asks `xdg-settings` for the default browser and reads that browser's profiles, including Flatpak and Snap installs. When it has several profiles, the most recently written session store wins. If the default can't be determined, as on macOS, d2l looks at every Firefox, Zen, LibreWolf and Floorp profile.

Chromium-family browsers encrypt their cookies, so d2l can't read them. If your default is one of those, or its profile can't be found, or you pass `--chromium`, d2l opens its own Chromium window instead. Once that window lands on Learn, d2l copies the session cookies and closes it. That browser keeps its own profile in `~/.local/state/d2l/browser`. If you picked "stay signed in" at SSO, later `--chromium` logins finish in a hidden browser. Pass `--chromium -i` to skip that attempt and show the window right away. d2l looks for Chromium, Chrome, Brave and Edge under their usual names. If yours lives somewhere else, set `browser` in the config.

The session is stored with mode 0600 in `~/.local/state/d2l/session.json`. Sessions expire. When one does, `d2l sync` first tries to renew it from your default browser's session, then from the saved Chromium profile. If neither works, or another command says the session expired, run `d2l login` again.

If the browser login does not work on your machine, for example over SSH with no display, copy the `d2lSessionVal` and `d2lSecureSessionVal` cookies from a logged-in browser tab and paste them in:

```sh
d2l login --cookie -
```

It asks for `d2lSessionVal=...; d2lSecureSessionVal=...` without echoing it. It also reads that line from a pipe. Passing the cookies as the flag value works too, but then they end up in your shell history.

`d2l logout` deletes the saved session. `d2l logout --all` also deletes the browser profile, so the next login starts from scratch.

## Usage

| Command | What it does |
| --- | --- |
| `d2l` | Open the TUI. |
| `d2l login [--chromium [-i]] [--cookie -]` | Log in and save the session. |
| `d2l logout [--all]` | Delete the session, and with `--all` the browser profile. |
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

# Chromium-family browser used by "d2l login". Leave unset to auto-detect.
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
