-- seq_and_check_resp.lua

local i_min, i_max = 1, 10000000
local i = i_min

local language = "en"
local domain = "1x001.com"
local project_id = "285"

function expected_json(i)
    local istr = tostring(i)
    return '{"data":{"type":"seo/pagedata","attributes":{"title":"1xBet[' .. istr .. ']: It repeats some phrases multiple times. This is a long description for SEO page data.","description":"1xBet[' .. istr .. ']: his is a long description for SEO page data. This description is intentionally made verbose to increase the JSON payload size.","metaRobots":[],"hierarchyMetaRobots":[{"name":"robots","content":"noindex, nofollow"}],"ampPageUrl":null,"alternativeLinks":[],"alternateMedia":[],"customCanonical":null,"metas":[],"siteName":null}}}'
end

request = function()
    local q =
        "?project[id]=" .. project_id ..
        "&domain=" .. domain ..
        "&language=" .. language ..
        "&choice[name]=betting" ..
        "&choice[choice][name]=betting_live" ..
        "&choice[choice][choice][name]=betting_live_null" ..
        "&choice[choice][choice][choice][name]=betting_live_null_" .. i ..
        "&choice[choice][choice][choice][choice][name]=betting_live_null_" .. i .. "_" .. i ..
        "&choice[choice][choice][choice][choice][choice][name]=betting_live_null_" .. i .. "_" .. i .. "_" .. i ..
        "&choice[choice][choice][choice][choice][choice][choice]=null"

    local path = "/api/v2/pagedata" .. q

    return wrk.format("GET", path, {
        ["Host"] = "0.0.0.0:8020",
        ["Accept-Encoding"] = "gzip, deflate, br",
        ["Accept-Language"] = "en-US,en;q=0.9",
        ["Content-Type"] = "application/json"
    })
end

response = function(status, headers, body)
    local expected = expected_json(i)
    if body ~= expected then
        print("[wrk][i=" .. i .. "] MISMATCH ❌")
        print("Expected: " .. expected)
        print("Actual:   " .. body)
    end

    -- цикл
    i = i + 1
    if i > i_max then
        i = i_min
    end
end
