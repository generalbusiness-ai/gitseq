

test("saved notification positions survive a remount and stay separate by room and actor", async () => {
  localStorage.clear();
  const ownKey = "workroom.foryou.genesis.codex-fingerprint";
  localStorage.setItem(ownKey, JSON.stringify({ watermark: 1, read: [] }));
  const vite = await createServer({ root: uiRoot, appType: "custom", logLevel: "silent", server: { middlewareMode: true } });
  let mounted;
  try {
    const { TopBar } = await vite.ssrLoadModule("/src/components/TopBar.tsx");
    async function mount(actor, genesis = "genesis") {
      if (mounted) await act(async () => mounted.unmount());
      mounted = createRoot(document.getElementById("root"));
      const workroom = addressed();
      workroom.status.durable.projection.statements[2].actor = "codex-fingerprint";
      workroom.status.durable.genesis = genesis;
      await act(async () => mounted.render(React.createElement(TopBar, {
        workroom, session: { actor, activity: { status: "available", focus: [] }, setActivity() {} },
        onJumpEvent() {}, onPublish() {},
      })));
    }
    const title = () => document.querySelector('button[aria-haspopup="menu"]').getAttribute("title");
    await mount("codex");
    assert.match(title(), /1 for you/, "mount did not load the independently seeded saved position");
    await click(document.querySelector('button[aria-haspopup="menu"]'));
    assert.match(document.querySelector('[role="menu"]').textContent, /Mentioning you about the gate/);
    assert.doesNotMatch(document.querySelector('[role="menu"]').textContent, /Repair the citation anchors/);
    await click(buttonByText((text) => text === "mark all read"));
    assert.deepEqual(JSON.parse(localStorage.getItem(ownKey)), { watermark: 2, read: [] }, "mark read did not persist through the component storage path");
    await mount("codex");
    assert.match(title(), /nothing for you/, "remount lost the saved read position");
    await mount("claude");
    assert.match(title(), /1 for you/, "another actor inherited the first actor's saved position");
    await mount("codex", "other-genesis");
    assert.match(title(), /2 for you/, "another room inherited the first room's saved position");
    assert.deepEqual(JSON.parse(localStorage.getItem(ownKey)), { watermark: 2, read: [] }, "scope changes overwrote the original position");
  } finally {
    if (mounted) await act(async () => mounted.unmount());
    await vite.close();
  }
});
