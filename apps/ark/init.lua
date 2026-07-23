-- Auto-display ark app on session creation.
-- Uses setImmediate to defer until after all Lua init files finish loading,
-- ensuring viewdefs are delivered when the browser is connected.
if not session.reloading then
    session:setImmediate(function()
        mcp:display("ark")
    end)
end

-- Launch stub: the Luhmann view's confirm dialog calls sys.luhmannLaunch().
-- The real Go verb (and the CLI-launch wake push that calls ark._luhmann:wake())
-- is PENDING #62. The nil-check means a Go-registered implementation wins
-- once it exists — delete this stub when #62 lands.
--
-- Deferred: `sys` does not exist yet while init files load — ark's
-- registerLuaFunctions() runs only after the ui engine is up (server.go),
-- and indexing it here crashed the whole UI start (found 2026-07-23).
-- Luhmann:confirmLaunch() guards for the stub being absent anyway.
session:setImmediate(function()
    if sys ~= nil and sys.luhmannLaunch == nil then
        function sys.luhmannLaunch()
            mcp:notify("Launching isn't wired up yet — run: ark luhmann launch", "warning")
        end
    end
end)
