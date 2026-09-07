---
title: Reading notes, source files, and evidence
summary: Find durable records and read the exact content their references name.
rests_on:
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:6cb46390f7cc0630f8f7518d79c3031c4b226605
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:109d5eb915643120959d224369327a034f6a5d43
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:87165f1520bdf1a58e390a53b939b310fcd12df9
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:a5ba7c376e9417d6c11f5275f47202179381a30e
  - git:sha1:5d2622748872b7e2dec3fe5c59e4be73a35e0bc8#git:sha1:599960fd61ab6d3288f3977f60ea80a0ae0ca5ea
---

# Reading notes, source files, and evidence

Choose **Notes** beside **Requests** to read statements whose kinds have
`render: note` in this workroom's declared vocabulary. Each row shows its
first line, author, number, date, kind, and any stale or retired state.
Retired notes remain readable. This view does not add notes to request counts
or change approval duties. Search matches note text, author names and numbers;
a large result shows its newest 200 entries and asks you to narrow the search.

In either view, enter an exact number such as `#19016` to open that durable
record, even when it is not a request or does not belong to the selected
request population. The exact-number result is separate from filtered rows.
A record known only through its fold decision still has a detail view.

## Follow a reference

Open a record's details to find its **evidence** attachments. Select an
attachment or a linked repository file to open a preview over the current
record. The preview names the repository, owning record and exact revision.
Markdown supports headings, paragraphs, lists, fenced code, inline code and
links. Unsupported formatting remains readable text. HTML stays text and
images are not fetched. JSON and other source files show line numbers. A
reference such as `internal/service/server.go:42` highlights line 42;
**Show source and line numbers** exposes Markdown's original lines too.

A record's own text and body fields may name that record's own evidence.
A name in the text, in a Markdown link or in a body value that is exactly
the name of one of this record's attachments opens that attachment — the
same content the evidence row opens under the same name. A reference that
says more than that bare name opens a repository file instead, so a source
path sharing an attachment's name stays reachable: an explicit revision
such as `notes.md@<full object ID>`, an explicit line such as `notes.md:12`,
or any other text that differs from every attachment name, including a
directory component no attachment carries. The artifact **path** row always
opens the source file at its commit, and the **evidence** row always opens
the attachment. The reader compares whole names and guesses nothing: no
other record, no other revision and no file outside this repository. Until
this record's attachment listing has arrived, and if it cannot be read,
every reference in the text resolves as a repository path.

A file uses the selected record's exact artifact or review head, or its
explicit `head` or `commit` field. With none of those, the reader considers
only directly cited artifacts. One distinct revision can be selected
automatically. Several require an explicit choice; none produce an explanation.
An explicitly supplied full commit must be among these cited revisions.
There is no current-branch or working-copy fallback. Evidence comes from the
owning signed event's attachment tree, independently of its source head.

The same dialog reports missing objects, absent paths, binary content,
unsupported symbolic links or submodules, and content too large to read.
A directory lists at most 128 entries and states how many were omitted.

A text file is shown in bounded windows. A file that fits in one window
(at most 400 lines, 64 KiB, no line over 4 KiB) is shown whole. A larger
file opens at the window containing the cited line, or at its first lines
without one; the dialog says which lines it is showing of how many, that
the view is partial, and offers **Previous lines** and **Next lines**. Line
numbers are the file's own. Windows are a fixed division of the file:
reading from line one, each window takes up to 400 lines and closes early
when the next line would take it past 64 KiB, so for ordinary files the
windows are the aligned 400-line runs and for files with long lines they
are shorter. A link naming any line opens the window holding it, so a
copied link returns to the same window later; **Next lines** and
**Previous lines** are the neighbouring windows, with no lines skipped or
repeated. A line over 4 KiB is cut at a character boundary and named as
cut. A cited line or window start beyond the file is reported
explicitly. Markdown reads as a document only when the whole file is shown;
a window is source with line numbers. Evidence attachments use the same
windows. The window a link names is part of the address (`from=`), so
reload, Back and Forward return to it.

Reading is bounded separately from showing. The resident reads and
hash-verifies at most 4 MiB of one file; a larger file is refused with no
content, and so is one that is not valid UTF-8 text. Git metadata has its own
bounds: 1 MiB for the commit, 4 MiB across trees, and at most 32 path
components. Eight previews may run at once, each with a ten-second read
deadline.

## Keep your place

Closing a preview or pressing Escape returns to the underlying reading view
and restores focus to its opener. Tab and Shift+Tab stay inside the dialog.
Opening a preview adds a browser history entry; Back and Forward revisit it.
A preview opened from a shared link closes to its underlying record without
leaving the application. Reload preserves the record, preview target and line.
Raw canonical record IDs and percent-encoded IDs both work in record and
focus links. Damaged encoding produces an error while preserving any valid
record portion of the address.

## Read boundary

`POST /v0/preview` is a read operation. It accepts one JSON object containing
`event` and either `path` with optional `commit`, or `attachment`. With only
`event`, it lists attachments and exact cited source heads. It accepts no
repository selector: every read belongs to the resident's repository and a
record in its verified projection. The request is limited to 8 KiB and uses
the resident's same-origin JSON checks.

Paths are literal repository paths. Absolute paths, traversal components,
revision expressions and backslashes are refused. Source reads verify Git
object hashes and ignore replacements, worktree contents and content filters.
No preview executes content, follows symbolic links, fetches images, changes
custody or grants signing authority. External links allow only HTTP and HTTPS.
