-- Auto-display ark app on session creation.
-- Uses setImmediate to defer until after all Lua init files finish loading,
-- ensuring viewdefs are delivered when the browser is connected.
if not session.reloading then
    session:setImmediate(function()
        mcp:display("ark")
    end)
end
