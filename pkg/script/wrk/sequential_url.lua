-- sequential_url.lua

-- Диапазоны
local sport_min, sport_max = 1, 1
local championship_min, championship_max = 1, 100
local match_min, match_max = 1, 100

-- Счетчики
local sport = sport_min
local championship = championship_min
local match = match_min

request = function()
    -- Формируем URL
    local q = "choice[name]=betting"
        .. "&choice[choice][name]=betting_live"
        .. "&choice[choice][choice]=betting_live_null"
        .. "&choice[choice][choice][choice]=betting_live_null_" .. sport
        .. "&choice[choice][choice][choice][choice]=betting_live_null_" .. sport .. "_" .. championship
        .. "&choice[choice][choice][choice][choice][choice]=betting_live_null_" .. sport .. "_" .. championship .. "_" .. match

    local path = "/api/v1/cache?language=en&domain=melbet-djibouti.com&timezone=3&project[id]=62&stream=homepage&" .. q

    -- Инкрементируем счетчики с переходом к началу диапазона
    match = match + 1
    if match > match_max then
        match = match_min
        championship = championship + 1
        if championship > championship_max then
            championship = championship_min
            sport = sport + 1
            if sport > sport_max then
                sport = sport_min
            end
        end
    end

    return wrk.format("GET", path)
end
