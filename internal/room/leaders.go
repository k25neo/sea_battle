package room

// Таблица лидеров: HTTP-ответ собирается здесь, хранилище остаётся нейтральным.

const leadersMinGames = 3 // без этого топ забивают однопартийные «везунчики»

type LeaderRow struct {
	Rank  int    `json:"rank"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Elo   int    `json:"elo"`
	Games int    `json:"games"`
	Wins  int    `json:"wins"`
}

type LeadersResp struct {
	Leaders []LeaderRow `json:"leaders"`
	Me      *LeaderRow  `json:"me"`
}

// Leaders — топ-лига по ЭЛО; meId (например "vk:123" или "g:uuid") получает
// строку «ты» с местом, даже если до топ-20 не дотянул.
func (m *Manager) Leaders(limit int, meID string) LeadersResp {
	resp := LeadersResp{Leaders: []LeaderRow{}}
	if m.store == nil {
		return resp
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	players, me, meRank, err := m.store.Leaders(limit, meID, leadersMinGames)
	if err != nil {
		return resp
	}
	for i, p := range players {
		resp.Leaders = append(resp.Leaders, LeaderRow{Rank: i + 1, ID: p.ID, Name: p.Name, Elo: p.Elo, Games: p.Games, Wins: p.Wins})
	}
	if me != nil {
		meRow := &LeaderRow{Rank: meRank, ID: me.ID, Name: me.Name, Elo: me.Elo, Games: me.Games, Wins: me.Wins}
		if me.Games < leadersMinGames {
			meRow.Rank = 0 // в зачёт рейтинга ещё не попал — клиент объяснит
		}
		resp.Me = meRow
	}
	return resp
}
