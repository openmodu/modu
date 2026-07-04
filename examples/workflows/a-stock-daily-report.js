meta({
  name: "A股每日分析报告",
  description: "分析今日A股市场总览、板块题材、资金成交、风险与明日关注，生成结构化报告",
  phases: [
    { title: "状态读取", detail: "读取上一轮 watchlist 并作为今日复盘输入" },
    { title: "数据采集", detail: "按数据源风控约束串行采集A股市场数据" },
    { title: "报告合成", detail: "汇总四路分析结果，合成最终报告" },
    { title: "状态更新", detail: "写回 state/watchlist.md，供下一轮对比复盘" },
  ],
});

const today = new Intl.DateTimeFormat("sv-SE", {
  timeZone: "Asia/Shanghai",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
}).format(new Date());
const weekday = new Intl.DateTimeFormat("en-US", {
  timeZone: "Asia/Shanghai",
  weekday: "short",
}).format(new Date());
const isWeekend = weekday === "Sat" || weekday === "Sun";
const dateCN = today.replace(/-/g, "年").slice(0, 4) + "年" + today.slice(5, 7) + "月" + today.slice(8, 10) + "日";
const DATA_SOURCE_RULES = `## 数据源规则
- 优先用 bash 执行 Python/urllib/requests 直连公开 API；web_search/web_fetch 只作补充交叉验证。
- 行情/指数优先腾讯财经 API（GBK 解码），不要为了行情去打东财。
- 同花顺热点、同花顺北向可直接 HTTP 获取。
- 东财只用于行业板块、龙虎榜、解禁、全球资讯等独有数据；所有东财请求必须串行，至少间隔 1 秒并带 User-Agent/Referer。
- 如果今天不是交易日或核心接口全部返回空，明确写"非交易日/暂无数据"，不要编造涨跌幅、成交额、题材或标的；不要创建或更新 state/watchlist.md，保留上一轮状态。`;

// ──────────────────────────────────────────────
// PHASE 0: 读取上一轮状态，让每日任务变成 feeds-itself loop
// ──────────────────────────────────────────────
phase("状态读取");

const previousWatchlist = await agent(
  `读取仓库中的 state/watchlist.md，作为今日A股复盘的上一轮状态。

要求：
1. 如果文件存在，原样读取并总结：
   - 上一轮日期
   - 昨日/上一轮关注的题材
   - 昨日/上一轮关注的标的
   - 需要今天验证的假设
2. 如果文件不存在，明确输出"无历史 watchlist，本轮建立基线"。
3. 不要改文件，只读。`,
  { label: "读取watchlist", phase: "状态读取", tools: ["read", "bash"], permissionMode: "read-only", maxTurns: 8 }
);

if (isWeekend) {
  return `# A股每日市场分析报告（${dateCN}）

今天是上海时区周末（${weekday}），A股不开市。本次运行只读取上一轮 watchlist，不采集行情、不更新 state/watchlist.md，避免把非交易日空数据写成有效观察。

## 上一轮 watchlist
${previousWatchlist}`;
}

// ──────────────────────────────────────────────
// PHASE 1: 按数据源风控串行采集数据
// ──────────────────────────────────────────────
phase("数据采集");

// ── Agent 1: 市场总览 ──
const marketReport = await agent(
    `你是A股市场分析专家。请分析今日（${dateCN}）A股市场总览情况。

## 任务

${DATA_SOURCE_RULES}

使用 bash 直连 API 完成以下数据采集和分析；必要时再用 web_search/web_fetch 补充来源：

### Step 1: 抓取实时数据
用 bash/Python 拉取腾讯行情数据：
- URL: https://qt.gtimg.cn/q=sh000001,sz399001,sz399006,sh000300,sh000688,sh000016,sh000905,sh000852
- 响应编码为 GBK，需要 decode('gbk')
- 每行格式: v_sh000001="1~上证指数~...字段~"
- 字段(0-based索引): 1=名称, 3=最新价, 4=昨收, 5=今开, 31=涨跌额, 32=涨跌幅%, 33=最高, 34=最低, 37=成交额(万), 38=换手率%, 39=PE_TTM, 44=总市值(亿), 45=流通市值(亿), 46=PB

### Step 2: 搜索市场情绪
搜索当日市场情绪新闻，关键词：
- "${today} A股涨停跌停家数"
- "今日A股市场总结 收盘综述"

## 输出要求
1. 主要指数行情表格（指数名、收盘价、涨跌幅、成交额）
2. 涨跌家数/涨停跌停数量（如有数据）
3. 今日市场特征总结（200字以内）：大小盘风格、成交量变化、市场情绪
4. **每个数据点标明来源URL**`,
    { label: "市场总览", phase: "数据采集", tools: ["web_search", "web_fetch", "bash"], permissionMode: "read-only", maxTurns: 25 });

// ── Agent 2: 板块/题材 ──
const sectorReport = await agent(
    `你是A股板块题材分析专家。请分析今日（${dateCN}）市场板块轮动和热点题材。

## 任务

${DATA_SOURCE_RULES}

使用 bash 直连 API 完成以下数据采集和分析；必要时再用 web_search/web_fetch 补充来源：

### Step 1: 抓取东财行业/概念板块数据
用 bash/Python 拉取东财 API，并遵守东财串行限流：
- URL: https://push2.eastmoney.com/api/qt/clist/get
- Query params: pn=1, pz=100, po=1, np=1, fltt=2, invt=2, fs=m:90+t:2, fields=f2,f3,f4,f12,f13,f14,f104,f105,f128,f136,f140,f141,f207
- 响应是JSON，解析 data.diff 列表
- 字段: f14=行业名称, f3=涨跌幅%, f104=上涨家数, f105=下跌家数, f140=领涨股, f136=领涨股涨幅%

再拉取概念板块排名（fs=m:90+t:3），其他参数同上。

### Step 2: 抓取同花顺热点数据
用 bash/Python 拉取同花顺强势股 API：
- URL: http://zx.10jqka.com.cn/event/api/getharden/date/${today}/orderby/date/orderway/desc/charset/GBK/
- 解析JSON中的data字段，提取每只股票的名称、涨幅%、题材归因(reason)

## 输出要求
1. 涨幅TOP8行业板块（涨跌幅、上涨家数/下跌家数、领涨股）
2. 跌幅TOP5行业板块
3. 热点概念板块TOP6
4. 今日核心主线（哪个板块最强、持续性如何）
5. 连板/涨停情况分析
6. **每个数据点标明来源URL**`,
    { label: "板块题材", phase: "数据采集", tools: ["web_search", "web_fetch", "bash"], permissionMode: "read-only", maxTurns: 25 });

// ── Agent 3: 资金/成交 ──
const capitalReport = await agent(
    `你是A股资金面分析专家。请分析今日（${dateCN}）市场资金流向和成交情况。

## 任务

${DATA_SOURCE_RULES}

### Step 1: 抓取北向资金数据
用 bash/Python 拉取同花顺北向资金 API：
- URL: https://data.hexin.cn/market/hsgtApi/method/dayChart/
- Headers: User-Agent=Mozilla/5.0, Referer=https://data.hexin.cn/
- 响应是JSON，解析 time/hgt/sgt 数组，取最后一条

### Step 2: 抓取龙虎榜数据
用 bash/Python 拉取东财龙虎榜，并遵守东财串行限流：
- URL: https://datacenter-web.eastmoney.com/api/data/v1/get
- Query params: reportName=RPT_DAILYBILLBOARD_DETAILSNEW, columns=SECURITY_CODE,SECURITY_NAME_ABBR,EXPLANATION,CLOSE_PRICE,CHANGE_RATE,BILLBOARD_NET_AMT,BILLBOARD_BUY_AMT,BILLBOARD_SELL_AMT,TURNOVERRATE,TRADE_DATE, filter=(TRADE_DATE>="${today}")(TRADE_DATE<="${today}"), pageSize=50, sortTypes=-1, sortColumns=BILLBOARD_NET_AMT, source=WEB, client=WEB
- 解析 result.data，打印净买入TOP5和净卖出TOP5

### Step 3: 搜索融资融券和主力资金
搜索关键词：
- "融资融券余额 ${today}"
- "主力资金净流入板块 ${today}"
- "今日A股资金流向排名"

### Step 4: 抓取市场总成交额
用 bash/Python 拉取腾讯行情获取成交额：
- URL: https://qt.gtimg.cn/q=sh000001,sz399001
- 字段索引37=成交额(万元)，根据索引解析

## 输出要求
1. 两市总成交额及较昨日变化（放量/缩量百分比）
2. 北向资金今日净流入/流出金额（沪股通+深股通分开）
3. 龙虎榜机构净买入/卖出TOP3
4. 主力资金流入/流出最多的板块
5. 融资融券余额趋势
6. 资金面判断（100字）：增量or存量？风格偏好？
7. **每个数据点标明来源URL**`,
    { label: "资金成交", phase: "数据采集", tools: ["web_search", "web_fetch", "bash"], permissionMode: "read-only", maxTurns: 25 });

// ── Agent 4: 风险与明日关注 ──
const riskReport = await agent(
    `你是A股风险预警与策略分析专家。请分析今日（${dateCN}）市场风险及明日关注点。

## 任务

${DATA_SOURCE_RULES}

使用 bash 直连公开 API 和 web_search/web_fetch 完成以下数据采集和分析：

### Step 1: 搜索今日重大新闻和外围市场
搜索关键词：
- "今日A股重大新闻 政策 ${today}"
- "美股今日行情 道琼斯 纳斯达克"
- "人民币汇率 今日 ${today}"
- "A股收盘综述 ${today}"

### Step 2: 抓取东财全球财经快讯
用 bash/Python 拉取东财7x24快讯，并遵守东财串行限流：
- URL: https://np-weblist.eastmoney.com/comm/web/getFastNewsList
- Query params: client=web, biz=web_724, fastColumn=102, sortEnd=, pageSize=50
- Headers: User-Agent=Mozilla/5.0, Referer=https://kuaixun.eastmoney.com/
- 解析 data.fastNewsList，阅读最近20条快讯，找出影响明日的关键信息

### Step 3: 搜索解禁信息
搜索关键词：
- "本周限售解禁股 ${today}"
- "A股解禁日历 下周"

### Step 4: 搜索明日关注
搜索关键词：
- "明日新股申购 新股${today.slice(0,7).replace('-','')}"
- "明日A股市场展望 策略"
- "${today.slice(0,7)}月A股投资策略 券商观点"

## 输出要求
1. 国内外重大事件/政策及对A股的影响
2. 外围市场（美股/港股/汇率/商品）表现和潜在影响
3. 明日新股/解禁/披露等日历提醒
4. 近期风险点（业绩预告/退市/监管等）
5. 明日重点关注板块（结合今日热点+事件催化）
6. 操作策略建议（仓位/方向/纪律）
7. **每个数据点标明来源URL**`,
    { label: "风险明日关注", phase: "数据采集", tools: ["web_search", "web_fetch", "bash"], permissionMode: "read-only", maxTurns: 25 });

// ──────────────────────────────────────────────
// PHASE 2: 合成最终报告
// ──────────────────────────────────────────────
phase("报告合成");

const finalReport = await agent(
  `你是一位资深的A股策略分析师。今天是${dateCN}。

请将以下四份分析报告合成为一份结构清晰、可读性强的每日A股市场分析报告。
同时结合上一轮 state/watchlist.md 做复盘：昨天/上一轮关注的题材和标的，今天是否得到验证、走弱、证伪或仍需观察。

## 报告格式要求

# A股每日市场分析报告（${dateCN}）

---

## 〇、昨日关注复盘
- 对上一轮 watchlist 里的题材逐条给出：延续/走弱/证伪/待观察
- 对上一轮 watchlist 里的标的逐条给出：表现、触发因素、是否仍保留
- 明确指出昨天判断里最错的一条和最有用的一条

## 一、市场总览
【核心观点】（2-3句话提炼）
- 主要指数表现（表格：指数名 | 收盘价 | 涨跌幅 | 成交额）
- 涨跌家数与市场情绪
- 今日市场特征总结

## 二、板块与题材轮动
【核心观点】（2-3句话提炼）
- 领涨行业板块TOP5
- 领跌行业板块TOP5
- 热点概念/题材分析
- 涨停板与连板情况
- 板块轮动方向判断

## 三、资金面分析
【核心观点】（2-3句话提炼）
- 两市成交额及变化
- 北向资金动向
- 龙虎榜机构动向
- 主力资金流向
- 融资融券趋势

## 四、风险提示与明日关注
- 国内外重要事件/数据
- 外围市场影响
- 解禁/新股等日历提醒
- 明日重点关注板块
- 操作策略建议

---

## 写作要求
1. 语言专业但不晦涩，观点鲜明
2. 有具体数据支撑，不空洞
3. 每个章节标注数据来源
4. 整体控制在1500字以内
5. 操作策略要有明确的仓位/方向建议
6. 复盘必须引用上一轮 watchlist 的具体题材/标的；没有历史 watchlist 时说明本轮是基线

## 上一轮 watchlist
${previousWatchlist}

## 以下是四份原始分析报告

### 【市场总览】
${marketReport}

### 【板块题材】
${sectorReport}

### 【资金成交】
${capitalReport}

### 【风险与明日关注】
${riskReport}

请合成最终报告：`,
  { label: "报告合成", phase: "报告合成", tools: [], permissionMode: "read-only", maxTurns: 15 }
);

// ──────────────────────────────────────────────
// PHASE 3: 写回 state/watchlist.md，下一轮开场会读回来复盘
// ──────────────────────────────────────────────
phase("状态更新");

const watchlistUpdate = await agent(
  `请根据今日最终报告和上一轮 watchlist，更新仓库文件 state/watchlist.md。

必须执行：
0. 如果今日最终报告明确为非交易日，或核心行情/热点/行业数据全部暂无数据，不要创建或更新 state/watchlist.md；只返回"非交易日/暂无数据，已保留上一轮 watchlist"。
1. 如果 state/ 不存在则创建。
2. 写入完整的新 state/watchlist.md，不要只追加流水账。
3. 文件必须使用 Markdown，包含以下固定结构：

# A股观察清单

## 最新日期
${today}

## 上一轮复盘
- 题材/标的：
- 今日验证：
- 结论：

## 今日发现的题材
| 题材 | 强度 | 证据 | 代表标的 | 明日验证点 |
| --- | --- | --- | --- | --- |

## 今日发现的标的
| 代码 | 名称 | 触发原因 | 所属题材 | 风险 | 明日验证点 |
| --- | --- | --- | --- | --- | --- |

## 明日观察假设
-

## 已移出观察
| 题材/标的 | 移出原因 | 日期 |
| --- | --- | --- |

约束：
- 今日发现的题材/标的必须来自最终报告中的证据，不要臆造。
- 今日发现的题材/标的表不得为空；如果没有有效交易数据或无法形成至少一个题材和一个标的，按非交易日/暂无数据处理，不要创建或更新 state/watchlist.md。
- 对上一轮 watchlist 的结论要写清"延续/走弱/证伪/待观察"。
- 明日验证点必须可观察，例如放量、连板、板块排名、北向/主力资金、公告/政策催化。
- 写完后读回 state/watchlist.md，确认文件存在且包含 ${today}。

## 上一轮 watchlist
${previousWatchlist}

## 今日最终报告
${finalReport}`,
  { label: "更新watchlist", phase: "状态更新", tools: ["read", "write", "bash"], maxTurns: 12 }
);

return finalReport + "\n\n---\n\n## Watchlist State Update\n" + watchlistUpdate;
