local limit = tonumber(arg[1]) or 16384
local buffer = ""
local truncated = false

local function emit_line()
    buffer = buffer:gsub("[%z\001-\010\011-\031\127]", "")
    buffer = buffer:gsub("\r$", "")
    if truncated then buffer = buffer .. " [truncated]" end
    io.write(buffer, "\n")
    io.flush()
    buffer = ""
    truncated = false
end

while true do
    local character = io.stdin:read(1)
    if not character then break end
    if character == "\n" then
        emit_line()
    elseif #buffer < limit then
        buffer = buffer .. character
    else
        truncated = true
    end
end
if #buffer > 0 or truncated then emit_line() end
