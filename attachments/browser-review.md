Independent browser review of cc24d6c43, 6 September 2026

The reviewer used a separate native Chrome tab against an owned read-only clone and candidate-built HTTP handler on port 17881. This fixture has no copied private keys. A synthetic noncustodial actor bypasses the join gate; the harness admits only GET and the preview/wait POST routes. This tests the candidate reader and real repository content, not live custody, signing, presence or resident deployment.

Observed journey: Requests -> Notes showed note titles, authors, numbers, dates and stale/retired labels, with an explicit newest-200 limit. Searching #19016 offered its exact record across views. Opening it and its detail exposed both evidence links. The Markdown link rendered the complete review in a scrollable preview with its owning event and exact event revision. Following its sibling JSON link rendered numbered JSON; reloading retained that evidence target. Browser Back returned to Markdown, Forward and Escape returned to the owning note.

A fully encoded artifact #19011 URL opened that artifact, not the table. Its internal/app/request_authoring.go link opened actual source at historical 21b7e74225885452ab0ad0dc49b33aaf2c326d54. A copied preview URL with line=42 visibly scrolled to and highlighted line 42. The API control separately compared these bytes with git show and refused an existing but uncited cc24 revision.

A malformed optional focus segment displayed an encoding error while retaining #19016. One initial native typeText navigation dropped the opening characters and became a search; an exact paste corrected that automation error. This is not classified as an application failure.

After opening the Markdown attachment from its detail, reverse Tab wrapped to the last sibling link, forward Tab returned to Close preview, and Escape restored focus to the original attachment link. Screenshots were visually inspected in the tool session; no screenshot files are claimed. The root test tab was closed afterward and the native actor's terminal Chess game tab remained intact.

Source and JSON are plain numbered text. Preview metadata makes the exact repository/revision explicit. The large standalone-note title and raw metadata remain verbose, but the bounded discovery/link/preview conditions are met; broader reading-density recommendations remain outside this slice.
