-- random_url.lua
math.randomseed(os.time())

request = function()
    local sport = math.random(1, 10)
    local championship = math.random(1, 100)
    local match = math.random(1, 100)
    local q = "choice[name]=betting&choice[choice][name]=betting_live&choice[choice][choice]=betting_live_null&choice[choice][choice][choice]=betting_live_null_".. sport .."&choice[choice][choice][choice][choice]=betting_live_null_".. sport .."_".. championship .."&choice[choice][choice][choice][choice][choice]=betting_live_null_".. sport .."_".. championship .."_".. match
    local path = "/api/v2/pagedata?language=en&domain=melbet-djibouti.com&timezone=3&project[id]=62&stream=homepage&" .. q
    return wrk.format("GET", path)
end