package storage

import (
	"context"
	"fmt"
	"github.com/Borislavv/advanced-cache/pkg/config"
	"github.com/Borislavv/advanced-cache/pkg/encoding/brotli"
	"github.com/Borislavv/advanced-cache/pkg/model"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"runtime"
	"strconv"
	"time"
)

var respTemplate = `{
    "data": {
        "type": "seo/pagedata",
        "attributes": {
            "title": "[%d] Treasure Tomb play online 👉 1xBet gambling games | 1xbet-ec.com",
            "description": "[%d] Treasure Tomb play online - 1xBet 🕹️ Best online gambling games 🕹️ Fast payout guarantee ✔️ Play Treasure Tomb for real money with 1xbet-ec.com",
            "metaRobots": [
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/team"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/team/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/long"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/long/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/long"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/long/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-live/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-live/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/alternative"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/alternative/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/alternative"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/alternative/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/cyber-zone"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/cyber-zone/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/chest"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/mobile/download-by-qr"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/eu2024/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/recommendation/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/dolgosrochnye"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/olympics2024/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/champs"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/champs/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/betsonyour"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/betsonyour/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/casino-lobby"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/casino-search"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/cyber/line/long/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/cyber/live/long/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/dolgosrochnye/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/favorites"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/aftermatch-vs-live"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/aftermatch-vs-live/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/cyber-stream/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/cyber-zone/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/aftermatch-vs-live"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/aftermatch-vs-live/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/cyber-stream"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/cyber-stream/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/lotto/game/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/marble/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/results"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/results/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/safe"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/search-events"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/statistic"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/statistic/team/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/statisticpopup/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-baseball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-baseball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-basketball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-basketball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-live"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/cyber-zone"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/cyber-stream"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-live"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/statistic/team"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-billiards/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-billiards"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-block-breaker/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-block-breaker"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-collision"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-collision/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-curling/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-curling"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-fidget-spinners/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-fidget-spinners"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-lotto/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-lotto"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-mma/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-mma"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-race/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-race"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-round-target/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-round-target"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-shooting/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-shooting"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-slides/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-slides"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-volleyball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-volleyball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-waves/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-waves"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-cricket/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-cricket"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-football/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-football"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-table-tennis/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-table-tennis"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-tennis/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-tennis"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-fifa/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-fifa"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-baccarat/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-baccarat"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-badminton/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-badminton"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-battleships/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-battleships"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-beach-volleyball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-beach-volleyball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-bicycle-racing/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-bicycle-racing"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-big-rumble-boxing/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-big-rumble-boxing"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-bowls/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-bowls"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-card-football/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-card-football"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-crystal/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-crystal"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-darts/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-darts"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-darts-live/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-darts-live"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-dice/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-dice"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-durak"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-durak/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-esports-bowling/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-esports-bowling"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-esports-horse-racing/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-esports-horse-racing"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-field-hockey/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-field-hockey"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-gigabash/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-gigabash"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-greyhound-racing/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-greyhound-racing"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-guilty-gear/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-guilty-gear"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-handball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-handball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-higher-vs-lower/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-higher-vs-lower"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-hyper-brawl/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-hyper-brawl"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-ice-hockey"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-ice-hockey/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-keirin/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-keirin"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-killer-joker/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-killer-joker"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-lottery/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-lottery"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-padel/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-padel"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-raid-shadow-legends/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-raid-shadow-legends"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-russian-lotto/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-russian-lotto"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-sega-football/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-sega-football"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-seka/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-seka"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-sette-e-mezzo/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-sette-e-mezzo"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-squash/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-squash"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-street-power-football/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-street-power-football"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-super-soccer-blast"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-super-soccer-blast/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-twentyone/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-twentyone"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-victory-formula/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-victory-formula"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-winter-olympics/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-winter-olympics"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-wwe-battlegrounds/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/live/marble-wwe-battlegrounds"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-baseball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-baseball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-basketball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-basketball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-billiards/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-billiards"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-block-breaker/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-block-breaker"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-collision"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-collision/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-curling/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-curling"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-fidget-spinners/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-fidget-spinners"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-lotto/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-lotto"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-mma/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-mma"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-race/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-race"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-round-target/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-round-target"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-shooting/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-shooting"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-slides/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-slides"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-volleyball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-volleyball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-waves/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-waves"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-cricket/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-cricket"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-football/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-football"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-table-tennis/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-table-tennis"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-tennis/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-tennis"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-fifa/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-fifa"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-baccarat/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-baccarat"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-badminton/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-badminton"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-battleships/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-battleships"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-beach-volleyball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-beach-volleyball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-bicycle-racing/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-bicycle-racing"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-big-rumble-boxing/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-big-rumble-boxing"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-bowls/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-bowls"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-card-football/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-card-football"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-crystal/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-crystal"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-darts/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-darts"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-darts-live/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-darts-live"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-dice/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-dice"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-durak"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-durak/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-esports-bowling/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-esports-bowling"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-esports-horse-racing/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-esports-horse-racing"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-field-hockey/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-field-hockey"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-gigabash/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-gigabash"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-greyhound-racing/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-greyhound-racing"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-guilty-gear/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-guilty-gear"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-handball/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-handball"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-higher-vs-lower/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-higher-vs-lower"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-hyper-brawl/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-hyper-brawl"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-ice-hockey"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-ice-hockey/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-keirin/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-keirin"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-killer-joker/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-killer-joker"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-lottery/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-lottery"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-padel/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-padel"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-raid-shadow-legends/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-raid-shadow-legends"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-russian-lotto/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-russian-lotto"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-sega-football/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-sega-football"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-seka/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-seka"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-sette-e-mezzo/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-sette-e-mezzo"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-squash/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-squash"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-street-power-football/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-street-power-football"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-super-soccer-blast"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-super-soccer-blast/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-twentyone/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-twentyone"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-victory-formula/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-victory-formula"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-winter-olympics/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-winter-olympics"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-wwe-battlegrounds/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/line/marble-wwe-battlegrounds"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/cyber/live/long"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/cyber/line/long"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/eu2024"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/olympics2024"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/champions"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/champions/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/slots/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/casino/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/cyber/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/epl"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/epl/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/laliga"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/laliga/*"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/europa-league-2025"
                },
                {
                    "name": "robots",
                    "content": "noindex, nofollow",
                    "urlPath": "/europa-league-2025/*"
                }
            ],
            "hierarchyMetaRobots": [],
            "ampPageUrl": null,
            "alternativeLinks": [
                {
                    "hrefLang": {
                        "href": "https://1xbet.et/aa",
                        "lang": "am-ET"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/ar",
                        "lang": "ar"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.dj/ar",
                        "lang": "ar-DJ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://eg1xbet.com/ar",
                        "lang": "ar-EG"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://so.1xbet.com/ar",
                        "lang": "ar-SO"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://tn.1xbet.com/ar",
                        "lang": "ar-TN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/az",
                        "lang": "az"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://aze.1xbet.com/az",
                        "lang": "az-AZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/bg",
                        "lang": "bg"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbetbd.com/bn",
                        "lang": "bn-BD"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/da",
                        "lang": "da"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/de",
                        "lang": "de"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/el",
                        "lang": "el"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/en",
                        "lang": "en"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://arg.1x-bet.com/en",
                        "lang": "en-AR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://aze.1xbet.com/en",
                        "lang": "en-AZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbetbd.com",
                        "lang": "en-BD"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.cm/en",
                        "lang": "en-CM"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://eg1xbet.com/en",
                        "lang": "en-EG"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.et/en",
                        "lang": "en-ET"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com.gh/en",
                        "lang": "en-GH"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ie.1xbet.com/en",
                        "lang": "en-IE"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ind.1xbet.com",
                        "lang": "en-IN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.co.ke/en",
                        "lang": "en-KE"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://kr.1xbet.com/en",
                        "lang": "en-KR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.kz/en",
                        "lang": "en-KZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-mn.com/en",
                        "lang": "en-MN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.mw/en",
                        "lang": "en-MW"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ng/en",
                        "lang": "en-NG"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbetnp.com/en",
                        "lang": "en-NP"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.co.nz/en",
                        "lang": "en-NZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ph.1xbet.mobi/en",
                        "lang": "en-PH"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet-pk.pk/en",
                        "lang": "en-PK"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.sl/en",
                        "lang": "en-SL"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://so.1xbet.com/en",
                        "lang": "en-SO"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://tn.1xbet.com/en",
                        "lang": "en-TN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ug",
                        "lang": "en-UG"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-uz.com/en",
                        "lang": "en-UZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-vi.com/en",
                        "lang": "en-VN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com.zm/en",
                        "lang": "en-ZM"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/es",
                        "lang": "es"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ar.1x-bet.com/es",
                        "lang": "es-AR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://chile.1xbet.com/es",
                        "lang": "es-CL"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://cr.1xbet.com/es",
                        "lang": "es-CR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ec/es",
                        "lang": "es-EC"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.hn/es",
                        "lang": "es-HN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com.mx/es",
                        "lang": "es-MX"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.pe/es",
                        "lang": "es-PE"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/et",
                        "lang": "et"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/fa",
                        "lang": "fa"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/fi",
                        "lang": "fi"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/fr",
                        "lang": "fr"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://bf.1xbet.com/fr",
                        "lang": "fr-BF"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.bi/fr",
                        "lang": "fr-BI"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.bj/fr",
                        "lang": "fr-BJ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.cd/fr",
                        "lang": "fr-CD"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ci/fr",
                        "lang": "fr-CI"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.cm/fr",
                        "lang": "fr-CM"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.dj/fr",
                        "lang": "fr-DJ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com.gn/fr",
                        "lang": "fr-GN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ht/ht",
                        "lang": "ht-HT"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.sn/fr",
                        "lang": "fr-SN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://tn.1xbet.com/fr",
                        "lang": "fr-TN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/he",
                        "lang": "he"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ind.1xbet.com/hi",
                        "lang": "hi-IN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/hr",
                        "lang": "hr"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/hu",
                        "lang": "hu"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/id",
                        "lang": "id"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/it",
                        "lang": "it"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbetjap.com/ja",
                        "lang": "ja-JP"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/ka",
                        "lang": "ka"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/kz",
                        "lang": "kk"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.kz/kz",
                        "lang": "kk-KZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/km",
                        "lang": "km"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/ko",
                        "lang": "ko"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://kr.1xbet.com/ko",
                        "lang": "ko-KR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/lv",
                        "lang": "lv"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/mk",
                        "lang": "mk"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/mn",
                        "lang": "mn"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.mn/mn",
                        "lang": "mn-MN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/ms",
                        "lang": "ms"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbetnp.com/sd",
                        "lang": "ne-NP"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/nb",
                        "lang": "no"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/pl",
                        "lang": "pl"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/pt",
                        "lang": "pt"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://bra.1xbet.com/br",
                        "lang": "pt-BR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/ro",
                        "lang": "ro"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbetmd.com/ro",
                        "lang": "ro-MD"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com",
                        "lang": "ru"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://aze.1xbet.com/ru",
                        "lang": "ru-AZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.kg/ru",
                        "lang": "ru-KG"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.kz",
                        "lang": "ru-KZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbetmd.com/ru",
                        "lang": "ru-MD"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-uz.com/ru",
                        "lang": "ru-UZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/sk",
                        "lang": "sk"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/al",
                        "lang": "sq"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/sr",
                        "lang": "sr"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/sv",
                        "lang": "sv"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/th",
                        "lang": "th"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/tr",
                        "lang": "tr"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/ua",
                        "lang": "uk"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet-pk.pk/ur",
                        "lang": "ur-PK"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/uz",
                        "lang": "uz"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-uz.com/uz",
                        "lang": "uz-UZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/vi",
                        "lang": "vi"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-vi.com/vi",
                        "lang": "vi-VN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com/cn",
                        "lang": "zh"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com.tw/cn",
                        "lang": "zh-CN"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet-ec.com/en",
                        "lang": "en-EC"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com.lr/en",
                        "lang": "en-LR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.com.lr/fr",
                        "lang": "fr-LR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.co.mz/pt",
                        "lang": "pt-MZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://tur.1xbet.com/tr",
                        "lang": "tr-TR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.tj/ru",
                        "lang": "ru-TJ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.tj/tj",
                        "lang": "tg-TJ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://dz.1xbet.com/ar",
                        "lang": "ar-DZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://dz.1xbet.com/en",
                        "lang": "en-DZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://dz.1xbet.com/fr",
                        "lang": "fr-DZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://bol.1xbet.com/es",
                        "lang": "es-BO"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ven.1xbet.com/es",
                        "lang": "es-VE"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.co.mz/en",
                        "lang": "en-MZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.gt/es",
                        "lang": "es-GT"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet-uy.com/es",
                        "lang": "es-UY"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://al.1xbet.com/en",
                        "lang": "en-AL"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://al.1xbet.com/al",
                        "lang": "sq-AL"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://aze.1xbet.com/tr",
                        "lang": "tr-AZ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://am.1xbet.com/ru",
                        "lang": "ru-AM"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://am.1xbet.com/en",
                        "lang": "en-AM"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ca.1xbet.com/en",
                        "lang": "en-CA"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ca.1xbet.com/fr",
                        "lang": "fr-CA"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://am.1xbet.com/hy",
                        "lang": "hy-AM"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.rs/bs",
                        "lang": "sr-RS"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.rs/en",
                        "lang": "en-RS"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbetmd.com/en",
                        "lang": "en-MD"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ma-1xbet.com/en",
                        "lang": "en-MA"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://om.1xbet.com/en",
                        "lang": "en-OM"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://om.1xbet.com/ar",
                        "lang": "ar-OM"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://kw.1xbet.com/en",
                        "lang": "en-KW"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://kw.1xbet.com/ar",
                        "lang": "ar-KW"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://mr.1xbet.com/en",
                        "lang": "en-MR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://mr.1xbet.com/ar",
                        "lang": "ar-MR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://mr.1xbet.com/fr",
                        "lang": "fr-MR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.dj/en",
                        "lang": "en-DJ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.lk/en",
                        "lang": "en-LK"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://sa.1xbet.com/en",
                        "lang": "en-SA"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://sa.1xbet.com/ar",
                        "lang": "ar-SA"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://qa.1xbet.com/en",
                        "lang": "en-QA"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://qa.1xbet.com/ar",
                        "lang": "ar-QA"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://bh.1xbet.com/en",
                        "lang": "en-BH"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://bh.1xbet.com/ar",
                        "lang": "ar-BH"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://tur.1xbet.com/en",
                        "lang": "en-TR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-cambodia.com/en",
                        "lang": "en-KH"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.fi/en",
                        "lang": "en-FI"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.fi/fi",
                        "lang": "fi-FI"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet-ge.com/ka",
                        "lang": "ka-GE"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet-ge.com/ru",
                        "lang": "ru-GE"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet-ge.com/en",
                        "lang": "en-GE"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ht/fr",
                        "lang": "fr-HT"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-id.com/en",
                        "lang": "en-ID"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-id.com/id",
                        "lang": "id-ID"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1x-bet-malaysia.com/en",
                        "lang": "en-MY"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ps/ar",
                        "lang": "ar-PS"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ps/en",
                        "lang": "en-PS"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://jo.1xbet.com/ar",
                        "lang": "ar-JO"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://jo.1xbet.com/en",
                        "lang": "en-JO"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://lb.1xbet.com/ar",
                        "lang": "ar-LB"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://lb.1xbet.com/en",
                        "lang": "en-LB"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ly/en",
                        "lang": "en-LY"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ly/ar",
                        "lang": "ar-LY"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://irq.1xbet.com/en",
                        "lang": "en-IQ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://irq.1xbet.com/ar",
                        "lang": "ar-IQ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ir.1xbet.com/en",
                        "lang": "en-IR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ir.1xbet.com/fa",
                        "lang": "fa-IR"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://sy.1xbet.com/en",
                        "lang": "en-SY"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://sy.1xbet.com/ar",
                        "lang": "ar-SY"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ye.1xbet.com/en",
                        "lang": "en-YE"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://ye.1xbet.com/ar",
                        "lang": "ar-YE"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://afg.1xbet.com/en",
                        "lang": "en-AF"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://afg.1xbet.com/fa",
                        "lang": "fa-AF"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.gm/en",
                        "lang": "en-GM"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.td/en",
                        "lang": "en-TD"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.td/fr",
                        "lang": "fr-TD"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.cg/en",
                        "lang": "en-CG"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.cg/fr",
                        "lang": "fr-CG"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ml/en",
                        "lang": "en-ML"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.ml/fr",
                        "lang": "fr-ML"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.gw/en",
                        "lang": "en-GW"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.gw/pt",
                        "lang": "pt-GW"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.mg/fr",
                        "lang": "fr-MG"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.cd/en",
                        "lang": "en-CD"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.gq/fr",
                        "lang": "fr-GQ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://1xbet.gq/en",
                        "lang": "en-GQ"
                    }
                },
                {
                    "hrefLang": {
                        "href": "https://bf.1xbet.com/en",
                        "lang": "en-BF"
                    }
                }
            ],
            "alternateMedia": [],
            "customCanonical": null,
            "metas": [],
            "siteName": null
        }
    }
}`

func LoadMocks(ctx context.Context, config config.Config, storage Storage, num int) {
	go func() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()

		log.Info().Msg("[mocks] mock data start loading")
		defer log.Info().Msg("[mocks] mocked data finished loading")

		path := []byte("/api/v2/pagedata")
		for entry := range streamEntryPointersConsecutive(ctx, config, path, num) {
			storage.Set(entry)
		}
	}()
}

func GetSingleMock(i int, path []byte, cfg config.Config) *model.Entry {
	query := make([]byte, 0, 512)
	query = append(query, []byte("project[id]=285")...)
	query = append(query, []byte("&domain=1x001.com")...)
	query = append(query, []byte("&language=en")...)
	query = append(query, []byte("&choice[name]=betting")...)
	query = append(query, []byte("&choice[choice][name]=betting_live")...)
	query = append(query, []byte("&choice[choice][choice][name]=betting_live_null")...)
	query = append(query, []byte("&choice[choice][choice][choice][name]=betting_live_null_"+strconv.Itoa(i))...)
	query = append(query, []byte("&choice[choice][choice][choice][choice][name]betting_live_null_"+strconv.Itoa(i)+"_"+strconv.Itoa(i))...)
	query = append(query, []byte("&choice[choice][choice][choice][choice][choice][name]betting_live_null_"+strconv.Itoa(i)+"_"+strconv.Itoa(i)+"_"+strconv.Itoa(i))...)
	query = append(query, []byte("&choice[choice][choice][choice][choice][choice][choice]=null")...)

	queryHeaders := [][2][]byte{
		{[]byte("Host"), []byte("0.0.0.0:8020")},
		{[]byte("Accept-Encoding"), []byte("gzip, deflate, br")},
		{[]byte("Accept-Language"), []byte("en-US,en;q=0.9")},
		{[]byte("Content-Type"), []byte("application/json")},
	}

	responseHeaders := [][2][]byte{
		{[]byte("Content-Type"), []byte("application/json")},
		{[]byte("Vary"), []byte("Accept-Encoding, Accept-Language")},
		{[]byte("Cache-Control"), []byte("no-cache")},
		{[]byte("Control-Transport"), []byte("tls")},
	}

	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()

	req.URI().SetPathBytes(path)
	req.URI().SetQueryStringBytes(query)

	for _, kv := range queryHeaders {
		req.Header.AddBytesKV(kv[0], kv[1])
	}

	for _, kv := range responseHeaders {
		resp.Header.AddBytesKV(kv[0], kv[1])
	}

	resp.SetStatusCode(200)
	resp.SetBody(copiedBodyBytes(i))
	resp.Header.SetLastModified(time.Now())

	entry, err := model.NewMockEntry(cfg, req, resp)
	if err != nil {
		panic(err)
	}

	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(resp)

	return entry
}

func streamEntryPointersConsecutive(ctx context.Context, cfg config.Config, path []byte, num int) <-chan *model.Entry {
	outCh := make(chan *model.Entry, runtime.GOMAXPROCS(0)*4)
	go func() {
		defer close(outCh)

		i := 1
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if i > num {
					return
				}
				query := make([]byte, 0, 512)
				query = append(query, []byte("project[id]=285")...)
				query = append(query, []byte("&domain=1x001.com")...)
				query = append(query, []byte("&language=en")...)
				query = append(query, []byte("&choice[name]=betting")...)
				query = append(query, []byte("&choice[choice][name]=betting_live")...)
				query = append(query, []byte("&choice[choice][choice][name]=betting_live_null")...)
				query = append(query, []byte("&choice[choice][choice][choice][name]=betting_live_null_"+strconv.Itoa(i))...)
				query = append(query, []byte("&choice[choice][choice][choice][choice][name]betting_live_null_"+strconv.Itoa(i)+"_"+strconv.Itoa(i))...)
				query = append(query, []byte("&choice[choice][choice][choice][choice][choice][name]betting_live_null_"+strconv.Itoa(i)+"_"+strconv.Itoa(i)+"_"+strconv.Itoa(i))...)
				query = append(query, []byte("&choice[choice][choice][choice][choice][choice][choice]=null")...)

				queryHeaders := [][2][]byte{
					{[]byte("Host"), []byte("0.0.0.0:8020")},
					{[]byte("Accept-Encoding"), []byte("gzip, deflate, br")},
					{[]byte("Accept-Language"), []byte("en-US,en;q=0.9")},
					{[]byte("Content-Type"), []byte("application/json")},
					{[]byte("Content-Encoding"), []byte("br")},
					{[]byte("Cache-Control"), []byte("max-age=2400, must-revalidate, public, s-maxage=3600, stale-if-error=86400, stale-while-revalidate=300")},
				}

				responseHeaders := [][2][]byte{
					{[]byte("Content-Type"), []byte("application/json")},
					{[]byte("Vary"), []byte("Accept-Encoding, Accept-Language")},
				}

				req := fasthttp.AcquireRequest()
				resp := fasthttp.AcquireResponse()

				req.URI().SetPathBytes(path)
				req.URI().SetQueryStringBytes(query)

				for _, kv := range queryHeaders {
					req.Header.AddBytesKV(kv[0], kv[1])
				}

				for _, kv := range responseHeaders {
					resp.Header.AddBytesKV(kv[0], kv[1])
				}

				resp.SetStatusCode(200)
				resp.SetBody(copiedBodyBytes(i))
				resp.Header.SetLastModified(time.Now())

				entry, err := model.NewMockEntry(cfg, req, resp)
				if err != nil {
					panic(err)
				}

				fasthttp.ReleaseRequest(req)
				fasthttp.ReleaseResponse(resp)

				outCh <- entry
				i++
			}
		}
	}()
	return outCh
}

// copiedBodyBytes returns a random ASCII string of length between minStrLen and maxStrLen.
func copiedBodyBytes(idx int) []byte {
	encoded, err := brotli.Encode([]byte(fmt.Sprintf(respTemplate, idx, idx)))
	if err != nil {
		panic(err)
	}
	return encoded
}
