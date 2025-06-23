-- sequential_url.lua

-- Диапазоны
local sport_min, sport_max = 1, 10
local championship_min, championship_max = 1, 100
local match_min, match_max = 1, 100

-- Счетчики
local sport = sport_min
local championship = championship_min
local match = match_min

-- Счётчик итераций
local iter = 0

request = function()
    -- Формируем URL часть q
    local q = "choice[name]=betting"
        .. "&choice[choice][name]=betting_live"
        .. "&choice[choice][choice][name]=betting_live_null"
        .. "&choice[choice][choice][choice][name]=betting_live_null_" .. sport
        .. "&choice[choice][choice][choice][choice][name]=betting_live_null_" .. sport .. "_" .. championship
        .. "&choice[choice][choice][choice][choice][choice][name]=betting_live_null_" .. sport .. "_" .. championship .. "_" .. match

    local path = "/api/v2/user?language=en&domain=google.com&timezone=3&project[id]=62&stream=homepage&" .. q

    -- Увеличиваем счётчик итераций
    iter = iter + 1

    -- Каждые 1000 итераций выводим сформированный путь
    if iter % 1000 == 0 then
        print(string.format("[wrk] Iteration %d: %s", iter, path))
    end

    -- Инкрементируем вложенные счётчики
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
