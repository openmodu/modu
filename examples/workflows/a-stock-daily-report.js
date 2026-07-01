meta({
  name: "A股每日分析报告",
  description: "分析今日A股市场总览、板块题材、资金成交、风险与明日关注，生成结构化报告",
  phases: [
    { title: "数据采集", detail: "4路Agent并行搜索并抓取A股市场数据" },
    { title: "报告合成", detail: "汇总四路分析结果，合成最终报告" },
  ],
});

const today = new Date().toISOString().slice(0, 10);
const dateCN = today.replace(/-/g, "年").slice(0, 4) + "年" + today.slice(5, 7) + "月" + today.slice(8, 10) + "日";

// ──────────────────────────────────────────────
// PHASE 1: 四路 agent 并行使用 web_search/web_fetch 采集数据
// ──────────────────────────────────────────────
phase("数据采集");

const [marketReport, sectorReport, capitalReport, riskReport] = await parallel([
  // ── Agent 1: 市场总览 ──
  () => agent(
    `你是A股市场分析专家。请分析今日（${dateCN}）A股市场总览情况。

## 任务

使用 web_search 和 web_fetch 完成以下数据采集和分析：

### Step 1: 搜索主要指数行情
先用 web_search 搜索当日上证指数、深证成指、创业板指、沪深300、科创50的收盘行情。

搜索关键词建议：
- "上证指数 今日收盘 ${today}"
- "A股主要指数行情 ${today}"
- "沪深300 创业板指 今日行情"

### Step 2: 抓取实时数据
用 web_fetch 拉取腾讯行情数据：
- URL: https://qt.gtimg.cn/q=sh000001,sz399001,sz399006,sh000300,sh000688,sh000016,sh000905,sh000852
- 响应编码为 GBK，需要 decode('gbk')
- 每行格式: v_sh000001="1~上证指数~...字段~"
- 字段(0-based索引): 1=名称, 3=最新价, 4=昨收, 5=今开, 31=涨跌额, 32=涨跌幅%, 33=最高, 34=最低, 37=成交额(万), 38=换手率%, 39=PE_TTM, 44=总市值(亿), 45=流通市值(亿), 46=PB

### Step 3: 搜索市场情绪
搜索当日市场情绪新闻，关键词：
- "${today} A股涨停跌停家数"
- "今日A股市场总结 收盘综述"

## 输出要求
1. 主要指数行情表格（指数名、收盘价、涨跌幅、成交额）
2. 涨跌家数/涨停跌停数量（如有数据）
3. 今日市场特征总结（200字以内）：大小盘风格、成交量变化、市场情绪
4. **每个数据点标明来源URL**`,
    { label: "市场总览", phase: "数据采集", tools: ["web_search", "web_fetch", "bash"], permissionMode: "read-only", maxTurns: 25 }),

  // ── Agent 2: 板块/题材 ──
  () => agent(
    `你是A股板块题材分析专家。请分析今日（${dateCN}）市场板块轮动和热点题材。

## 任务

使用 web_search 和 web_fetch 完成以下数据采集和分析：

### Step 1: 搜索行业板块排名
搜索关键词：
- "今日行业板块涨跌幅排名 ${today}"
- "A股板块涨幅榜 今日"
- "东财行业板块排名"

### Step 2: 抓取东财行业/概念板块数据
用 web_fetch 拉取东财API：
- URL: https://push2.eastmoney.com/api/qt/clist/get
- Query params: pn=1, pz=100, po=1, np=1, fltt=2, invt=2, fs=m:90+t:2, fields=f2,f3,f4,f12,f13,f14,f104,f105,f128,f136,f140,f141,f207
- 响应是JSON，解析 data.diff 列表
- 字段: f14=行业名称, f3=涨跌幅%, f104=上涨家数, f105=下跌家数, f140=领涨股, f136=领涨股涨幅%

再拉取概念板块排名（fs=m:90+t:3），其他参数同上。

### Step 3: 搜索热点题材/强势股
搜索关键词：
- "今日热点题材 概念板块 ${today}"
- "同花顺热点 ${today}"
- "今日涨停板 连板股"

### Step 4: 抓取同花顺热点数据
用 web_fetch 拉取同花顺强势股API：
- URL: http://zx.10jqka.com.cn/event/api/getharden/date/${today}/orderby/date/orderway/desc/charset/GBK/
- 解析JSON中的data字段，提取每只股票的名称、涨幅%、题材归因(reason)

## 输出要求
1. 涨幅TOP8行业板块（涨跌幅、上涨家数/下跌家数、领涨股）
2. 跌幅TOP5行业板块
3. 热点概念板块TOP6
4. 今日核心主线（哪个板块最强、持续性如何）
5. 连板/涨停情况分析
6. **每个数据点标明来源URL**`,
    { label: "板块题材", phase: "数据采集", tools: ["web_search", "web_fetch", "bash"], permissionMode: "read-only", maxTurns: 25 }),

  // ── Agent 3: 资金/成交 ──
  () => agent(
    `你是A股资金面分析专家。请分析今日（${dateCN}）市场资金流向和成交情况。

## 任务

使用 web_search 和 web_fetch 完成以下数据采集和分析：

### Step 1: 搜索成交额和北向资金
搜索关键词：
- "今日A股成交额 ${today}"
- "北向资金今日净流入 ${today}"
- "沪深港通资金流向 ${today}"

### Step 2: 抓取北向资金数据
用 web_fetch 拉取同花顺北向资金API：
- URL: https://data.hexin.cn/market/hsgtApi/method/dayChart/
- Headers: User-Agent=Mozilla/5.0, Referer=https://data.hexin.cn/
- 响应是JSON，解析 time/hgt/sgt 数组，取最后一条

### Step 3: 抓取龙虎榜数据
用 web_fetch 拉取东财龙虎榜：
- URL: https://datacenter-web.eastmoney.com/api/data/v1/get
- Query params: reportName=RPT_DAILYBILLBOARD_DETAILSNEW, columns=SECURITY_CODE,SECURITY_NAME_ABBR,EXPLANATION,CLOSE_PRICE,CHANGE_RATE,BILLBOARD_NET_AMT,BILLBOARD_BUY_AMT,BILLBOARD_SELL_AMT,TURNOVERRATE,TRADE_DATE, filter=(TRADE_DATE>="${today}")(TRADE_DATE<="${today}"), pageSize=50, sortTypes=-1, sortColumns=BILLBOARD_NET_AMT, source=WEB, client=WEB
- 解析 result.data，打印净买入TOP5和净卖出TOP5

### Step 4: 搜索融资融券和主力资金
搜索关键词：
- "融资融券余额 ${today}"
- "主力资金净流入板块 ${today}"
- "今日A股资金流向排名"

### Step 5: 抓取市场总成交额
用 web_fetch 拉取腾讯行情获取成交额：
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
    { label: "资金成交", phase: "数据采集", tools: ["web_search", "web_fetch", "bash"], permissionMode: "read-only", maxTurns: 25 }),

  // ── Agent 4: 风险与明日关注 ──
  () => agent(
    `你是A股风险预警与策略分析专家。请分析今日（${dateCN}）市场风险及明日关注点。

## 任务

使用 web_search 和 web_fetch 完成以下数据采集和分析：

### Step 1: 搜索今日重大新闻和外围市场
搜索关键词：
- "今日A股重大新闻 政策 ${today}"
- "美股今日行情 道琼斯 纳斯达克"
- "人民币汇率 今日 ${today}"
- "A股收盘综述 ${today}"

### Step 2: 抓取东财全球财经快讯
用 web_fetch 拉取东财7x24快讯：
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
    { label: "风险明日关注", phase: "数据采集", tools: ["web_search", "web_fetch", "bash"], permissionMode: "read-only", maxTurns: 25 }),
]);

// ──────────────────────────────────────────────
// PHASE 2: 合成最终报告
// ──────────────────────────────────────────────
phase("报告合成");

const finalReport = await agent(
  `你是一位资深的A股策略分析师。今天是${dateCN}。

请将以下四份分析报告合成为一份结构清晰、可读性强的每日A股市场分析报告。

## 报告格式要求

# A股每日市场分析报告（${dateCN}）

---

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

return finalReport;
