import { useState, useEffect, useCallback, useMemo } from 'react';
import {
  AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer,
  BarChart, Bar, Cell,
} from 'recharts';
import { api } from '../api';
import { DashboardData, PerformanceStats, AccountInfo, TradeInfo, PositionInfo } from '../types';

function MIcon({ name, filled, className }: { name: string; filled?: boolean; className?: string }) {
  return (
    <span
      className={`material-symbols-outlined ${className || ''}`}
      style={filled ? { fontVariationSettings: "'FILL' 1" } : undefined}
    >
      {name}
    </span>
  );
}

function isPositiveOutcome(side: string) {
  return ['YES', 'BUY', 'UP'].includes(side.toUpperCase());
}

export default function Portfolio() {
  const [dash, setDash] = useState<DashboardData | null>(null);
  const [perf, setPerf] = useState<PerformanceStats | null>(null);
  const [account, setAccount] = useState<AccountInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [activeTab, setActiveTab] = useState<'positions' | 'trades' | 'log'>('positions');
  const [timeRange, setTimeRange] = useState('30D');

  const load = useCallback(async () => {
    try {
      const [d, p, a] = await Promise.all([
        api.getDashboard(),
        api.getPerformance(),
        api.getAccount(),
      ]);
      setDash(d);
      setPerf(p);
      setAccount(a);
    } catch { /* fallback to empty */ }
    setLoading(false);
  }, []);

  useEffect(() => {
    load();
    api.connectWebSocket((type) => {
      if (type === 'dashboard' || type === 'trade' || type === 'position') load();
    });
    return () => {};
  }, [load]);

  const strategy = dash?.strategy;
  const trades = dash?.recent_trades || [];
  const positions = dash?.positions || [];
  const activePositions = positions.filter(p => p.is_active);
  const alerts = dash?.alerts || [];

  const equityData = useMemo(() => {
    if (account?.equity_curve?.length) {
      return account.equity_curve.map(pt => ({
        date: pt.date,
        value: pt.value,
      }));
    }
    return [];
  }, [account]);

  const weeklyData = useMemo(() => {
    if (account?.daily_stats?.length) {
      const dayNames = ['SUN', 'MON', 'TUE', 'WED', 'THU', 'FRI', 'SAT'];
      return account.daily_stats.slice(-7).map(d => {
        const dt = new Date(d.date + 'T00:00:00');
        return {
          day: dayNames[dt.getDay()] || d.date.slice(5),
          pnl: d.pnl,
        };
      });
    }
    return [];
  }, [account]);

  const bestDay = useMemo(() => {
    if (!weeklyData.length) return null;
    return weeklyData.reduce((best, d) => d.pnl > best.pnl ? d : best, weeklyData[0]);
  }, [weeklyData]);

  const worstDay = useMemo(() => {
    if (!weeklyData.length) return null;
    return weeklyData.reduce((worst, d) => d.pnl < worst.pnl ? d : worst, weeklyData[0]);
  }, [weeklyData]);

  // KPI values
  const totalPnl = perf?.total_pnl ?? strategy?.total_pnl ?? 0;
  const winRate = perf?.win_rate ?? strategy?.win_rate ?? 0;
  const maxDrawdown = perf?.max_drawdown ?? strategy?.drawdown ?? 0;
  const exposure = account && account.portfolio_value > 0
    ? ((account.positions_value / account.portfolio_value) * 100)
    : null;
  const totalTrades = perf?.total_trades ?? strategy?.total_trades ?? 0;
  const sharpeRatio = perf?.sharpe_ratio ?? 0;
  const portfolioHealth = Math.max(0, Math.min(100, 100 - maxDrawdown * 2));

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-slate-400 text-sm font-medium">Loading dashboard...</div>
      </div>
    );
  }

  return (
    <div className="space-y-8">
      {/* KPI Cards */}
      <section className="grid grid-cols-1 md:grid-cols-3 lg:grid-cols-5 gap-6">
        <KpiCard
          icon="monetization_on" iconBg="bg-primary/10" iconColor="text-primary"
          label="Total P&L"
          value={`${totalPnl >= 0 ? '+' : ''}$${totalPnl.toFixed(2)}`}
          trend={totalPnl >= 0 ? `${winRate.toFixed(1)}% win rate` : 'Needs improvement'}
          trendColor={totalPnl >= 0 ? 'text-secondary' : 'text-error'}
          trendIcon={totalPnl >= 0 ? 'trending_up' : 'trending_down'}
        />
        <KpiCard
          icon="bolt" iconBg="bg-secondary/10" iconColor="text-secondary"
          label="Win Rate"
          value={`${winRate.toFixed(1)}%`}
          trend={`${totalTrades} total trades`}
          trendColor="text-secondary"
          trendIcon="add"
        />
        <KpiCard
          icon="south_east" iconBg="bg-orange-100" iconColor="text-orange-600"
          label="Max Drawdown"
          value={`-${maxDrawdown.toFixed(1)}%`}
          trend={maxDrawdown > 5 ? 'Above threshold' : 'Within limits'}
          trendColor={maxDrawdown > 5 ? 'text-error' : 'text-secondary'}
        />
        <KpiCard
          icon="pie_chart" iconBg="bg-blue-100" iconColor="text-blue-600"
          label="Current Exposure %"
          value={exposure === null ? '--' : `${exposure.toFixed(0)}%`}
          trend={exposure === null ? 'Awaiting wallet data' : 'Utilized capital'}
          trendColor="text-slate-500"
        />
        <KpiCard
          icon="settings_input_component" iconBg="bg-violet-100" iconColor="text-violet-600"
          label="Active Strategies"
          value={strategy?.status === 'running' ? '1' : '0'}
          trend={strategy?.status === 'running' ? '1 Running' : 'None active'}
          trendColor="text-slate-500"
        />
      </section>

      {/* Main grid */}
      <div className="grid grid-cols-12 gap-8">
        {/* Left column */}
        <div className="col-span-12 lg:col-span-8 space-y-8">
          {/* Equity Curve */}
          <div className="bg-surface-container-lowest p-8 rounded-[2.5rem] neo-shadow relative overflow-hidden">
            <div className="flex justify-between items-center mb-8">
              <div>
                <h2 className="text-xl font-black text-slate-900">Equity Curve (30D)</h2>
                <p className="text-sm text-slate-400 font-medium">Portfolio performance tracking</p>
              </div>
              <div className="flex gap-2 p-1 bg-surface-container-low rounded-xl">
                {['1D', '7D', '30D', 'YTD', 'MAX'].map(r => (
                  <button
                    key={r}
                    onClick={() => setTimeRange(r)}
                    className={`px-3 py-1.5 text-xs font-bold rounded-lg transition-all ${
                      timeRange === r
                        ? 'bg-white text-primary shadow-sm'
                        : 'text-slate-500 hover:text-slate-900'
                    }`}
                  >
                    {r}
                  </button>
                ))}
              </div>
            </div>
            <div className="h-64 w-full">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={equityData} margin={{ top: 5, right: 20, left: 0, bottom: 5 }}>
                  <defs>
                    <linearGradient id="equityGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#7C3AED" stopOpacity={0.2} />
                      <stop offset="100%" stopColor="#7C3AED" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#eaeef2" />
                  <XAxis dataKey="date" tick={{ fontSize: 10, fill: '#94a3b8' }} axisLine={false} tickLine={false} />
                  <YAxis tick={{ fontSize: 10, fill: '#94a3b8' }} axisLine={false} tickLine={false} width={50} />
                  <Tooltip
                    contentStyle={{
                      background: 'rgba(255,255,255,0.9)',
                      backdropFilter: 'blur(12px)',
                      border: '1px solid #eaeef2',
                      borderRadius: '12px',
                      fontSize: '12px',
                      fontWeight: 600,
                    }}
                  />
                  <Area
                    type="monotone"
                    dataKey="value"
                    stroke="#7C3AED"
                    strokeWidth={3}
                    fill="url(#equityGrad)"
                    dot={false}
                    activeDot={{ r: 5, fill: '#7C3AED', stroke: '#fff', strokeWidth: 2 }}
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </div>

          {/* Positions / Trades / Log tabs */}
          <div className="bg-surface-container-lowest p-8 rounded-[2.5rem] neo-shadow border border-white/50">
            <div className="flex items-center gap-8 mb-6 border-b border-slate-100">
              {([
                { id: 'positions' as const, label: 'Open Positions' },
                { id: 'trades' as const, label: 'Trade History' },
                { id: 'log' as const, label: 'Execution Log' },
              ]).map(tab => (
                <button
                  key={tab.id}
                  onClick={() => setActiveTab(tab.id)}
                  className={`pb-4 text-sm font-bold transition-colors ${
                    activeTab === tab.id
                      ? 'font-black text-primary border-b-2 border-primary'
                      : 'text-slate-400 hover:text-slate-600'
                  }`}
                >
                  {tab.label}
                </button>
              ))}
            </div>
            <div className="overflow-x-auto">
              {activeTab === 'positions' && <PositionsTable positions={activePositions} />}
              {activeTab === 'trades' && <TradesTable trades={trades} />}
              {activeTab === 'log' && <LogTable alerts={alerts} />}
            </div>
          </div>
        </div>

        {/* Right column */}
        <div className="col-span-12 lg:col-span-4 space-y-8">
          {/* Weekly Performance */}
          <div className="bg-surface-container-lowest p-8 rounded-[2.5rem] neo-shadow border border-white/50">
            <h2 className="text-xl font-black text-slate-900 mb-6">Weekly Performance</h2>
            <div className="h-48">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={weeklyData} margin={{ top: 10, right: 0, left: 0, bottom: 0 }}>
                  <XAxis dataKey="day" tick={{ fontSize: 10, fontWeight: 700, fill: '#94a3b8' }} axisLine={false} tickLine={false} />
                  <Tooltip
                    contentStyle={{
                      background: 'rgba(255,255,255,0.9)',
                      border: '1px solid #eaeef2',
                      borderRadius: '12px',
                      fontSize: '12px',
                      fontWeight: 700,
                    }}
                    formatter={(value) => [`$${Number(value).toFixed(2)}`, 'P&L']}
                  />
                  <Bar dataKey="pnl" radius={[6, 6, 0, 0]}>
                    {weeklyData.map((entry, idx) => (
                      <Cell key={idx} fill={entry.pnl >= 0 ? '#006c49' : '#ba1a1a'} fillOpacity={0.8} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
            <div className="mt-8 space-y-3">
              {bestDay && (
                <div className="flex justify-between items-center p-3 bg-surface-container-low rounded-2xl">
                  <span className="text-xs font-bold text-slate-500 uppercase">Best Day</span>
                  <span className="text-xs font-black text-secondary">
                    {bestDay.day} (+${bestDay.pnl.toFixed(2)})
                  </span>
                </div>
              )}
              {worstDay && (
                <div className="flex justify-between items-center p-3 bg-surface-container-low rounded-2xl">
                  <span className="text-xs font-bold text-slate-500 uppercase">Worst Day</span>
                  <span className="text-xs font-black text-error">
                    {worstDay.day} (${worstDay.pnl.toFixed(2)})
                  </span>
                </div>
              )}
            </div>
          </div>

          {/* Risk Monitor */}
          <div className="bg-gradient-to-br from-slate-900 to-slate-950 p-8 rounded-[2.5rem] shadow-2xl text-white">
            <div className="flex items-center gap-2 mb-8">
              <div className="w-2 h-2 rounded-full bg-secondary animate-ping" />
              <h2 className="text-sm font-black tracking-[0.2em] text-slate-400 uppercase">Risk Monitor</h2>
            </div>
            <div className="space-y-6">
              <RiskRow icon="warning" label="Max Drawdown" value={`-${maxDrawdown.toFixed(1)}%`} valueColor="text-red-400" />
              <RiskRow icon="account_balance" label="Daily Loss Limit" value={`$${Math.abs(strategy?.daily_pnl || 0).toFixed(2)} / $250`} valueColor="text-slate-100" />
              <RiskRow icon="analytics" label="Sharpe Ratio" value={sharpeRatio.toFixed(2)} valueColor="text-emerald-400" badge={sharpeRatio > 1 ? 'Good' : 'Low'} />
              <RiskRow icon="reorder" label="Total Trades" value={String(totalTrades)} valueColor="text-slate-100" />

              <div className="pt-6 border-t border-slate-800">
                <div className="flex justify-between items-end mb-2">
                  <span className="text-xs font-bold text-slate-400">Portfolio Health</span>
                  <span className="text-sm font-black text-emerald-400">{portfolioHealth.toFixed(0)}%</span>
                </div>
                <div className="w-full h-2 bg-slate-800 rounded-full overflow-hidden">
                  <div
                    className="h-full bg-secondary rounded-full"
                    style={{ width: `${portfolioHealth}%`, boxShadow: '0 0 10px #10B981' }}
                  />
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

/* Sub-components */

function KpiCard({ icon, iconBg, iconColor, label, value, trend, trendColor, trendIcon }: {
  icon: string; iconBg: string; iconColor: string;
  label: string; value: string; trend: string; trendColor: string; trendIcon?: string;
}) {
  return (
    <div className="bg-surface-container-lowest p-5 rounded-[2rem] neo-shadow border border-white/50">
      <div className="flex justify-between items-start mb-4">
        <div className={`p-2 ${iconBg} rounded-xl ${iconColor}`}>
          <MIcon name={icon} filled />
        </div>
      </div>
      <p className="text-slate-500 font-semibold text-xs uppercase tracking-wider">{label}</p>
      <h3 className="text-2xl font-black text-on-surface mt-1">{value}</h3>
      <p className={`text-xs font-bold mt-2 flex items-center gap-1 ${trendColor}`}>
        {trendIcon && <MIcon name={trendIcon} className="text-sm" />}
        {trend}
      </p>
    </div>
  );
}

function RiskRow({ icon, label, value, valueColor, badge }: {
  icon: string; label: string; value: string; valueColor: string; badge?: string;
}) {
  return (
    <div className="flex justify-between items-center group">
      <div className="flex items-center gap-3">
        <MIcon name={icon} className="text-slate-500 group-hover:text-primary transition-colors" />
        <span className="text-xs font-medium text-slate-300">{label}</span>
      </div>
      <div className="flex items-center gap-2">
        <span className={`text-sm font-bold ${valueColor}`}>{value}</span>
        {badge && (
          <span className="text-[10px] px-1.5 py-0.5 bg-secondary/20 text-emerald-400 rounded">{badge}</span>
        )}
      </div>
    </div>
  );
}

function PositionsTable({ positions }: { positions: PositionInfo[] }) {
  if (!positions.length) {
    return <div className="text-center py-8 text-slate-400 text-sm">No open positions</div>;
  }
  return (
    <table className="w-full text-left">
      <thead>
        <tr className="text-[10px] font-bold text-slate-400 uppercase tracking-widest">
          <th className="pb-4 px-2">Market</th>
          <th className="pb-4 px-2">Side</th>
          <th className="pb-4 px-2">Quantity</th>
          <th className="pb-4 px-2">Avg. Price</th>
          <th className="pb-4 px-2">Current</th>
          <th className="pb-4 px-2 text-right">P&L (%)</th>
        </tr>
      </thead>
      <tbody className="text-sm">
        {positions.map((p, i) => (
          <tr key={i} className="border-b border-slate-50 hover:bg-slate-50/50 transition-colors">
            <td className="py-4 px-2 font-bold text-slate-900">{p.market_name || p.market_id}</td>
            <td className="py-4 px-2">
              <span className={`px-2 py-1 text-[10px] font-bold rounded-lg ${
                isPositiveOutcome(p.side)
                  ? 'bg-secondary/10 text-secondary'
                  : 'bg-error/10 text-error'
              }`}>
                {p.side.toUpperCase()}
              </span>
            </td>
            <td className="py-4 px-2 font-medium">{p.size.toFixed(2)}</td>
            <td className="py-4 px-2 text-slate-500">${p.entry_price.toFixed(2)}</td>
            <td className="py-4 px-2 text-slate-500">${p.current_price.toFixed(2)}</td>
            <td className={`py-4 px-2 text-right font-black ${p.pnl_pct >= 0 ? 'text-secondary' : 'text-error'}`}>
              {p.pnl_pct >= 0 ? '+' : ''}{p.pnl_pct.toFixed(1)}%
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function TradesTable({ trades }: { trades: TradeInfo[] }) {
  if (!trades.length) {
    return <div className="text-center py-8 text-slate-400 text-sm">No recent trades</div>;
  }
  return (
    <table className="w-full text-left">
      <thead>
        <tr className="text-[10px] font-bold text-slate-400 uppercase tracking-widest">
          <th className="pb-4 px-2">Time</th>
          <th className="pb-4 px-2">Market</th>
          <th className="pb-4 px-2">Direction</th>
          <th className="pb-4 px-2">Price</th>
          <th className="pb-4 px-2">Size</th>
          <th className="pb-4 px-2 text-right">P&L</th>
        </tr>
      </thead>
      <tbody className="text-sm">
        {trades.slice(0, 10).map((t, i) => (
          <tr key={i} className="border-b border-slate-50 hover:bg-slate-50/50 transition-colors">
            <td className="py-4 px-2 text-slate-500 text-xs">{new Date(t.timestamp).toLocaleString()}</td>
            <td className="py-4 px-2 font-bold text-slate-900">{t.market_name || t.market_id}</td>
            <td className="py-4 px-2">
              <span className={`px-2 py-1 text-[10px] font-bold rounded-lg ${
                t.direction === 'UP' || t.side === 'buy'
                  ? 'bg-secondary/10 text-secondary'
                  : 'bg-error/10 text-error'
              }`}>
                {t.direction || t.side}
              </span>
            </td>
            <td className="py-4 px-2 text-slate-500">${t.price.toFixed(2)}</td>
            <td className="py-4 px-2 font-medium">${t.total.toFixed(2)}</td>
            <td className={`py-4 px-2 text-right font-black ${t.pnl >= 0 ? 'text-secondary' : 'text-error'}`}>
              {t.pnl >= 0 ? '+' : ''}${t.pnl.toFixed(2)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function LogTable({ alerts }: { alerts: { id: string; timestamp: string; type: string; level: string; message: string }[] }) {
  if (!alerts.length) {
    return <div className="text-center py-8 text-slate-400 text-sm">No execution logs</div>;
  }
  return (
    <table className="w-full text-left">
      <thead>
        <tr className="text-[10px] font-bold text-slate-400 uppercase tracking-widest">
          <th className="pb-4 px-2">Time</th>
          <th className="pb-4 px-2">Level</th>
          <th className="pb-4 px-2">Type</th>
          <th className="pb-4 px-2">Message</th>
        </tr>
      </thead>
      <tbody className="text-sm">
        {alerts.slice(0, 10).map((a, i) => (
          <tr key={i} className="border-b border-slate-50 hover:bg-slate-50/50 transition-colors">
            <td className="py-3 px-2 text-slate-500 text-xs">{new Date(a.timestamp).toLocaleString()}</td>
            <td className="py-3 px-2">
              <span className={`px-2 py-1 text-[10px] font-bold rounded-lg ${
                a.level === 'error' ? 'bg-error/10 text-error'
                  : a.level === 'warning' ? 'bg-orange-100 text-orange-600'
                  : 'bg-blue-100 text-blue-600'
              }`}>
                {a.level.toUpperCase()}
              </span>
            </td>
            <td className="py-3 px-2 font-medium text-slate-700">{a.type}</td>
            <td className="py-3 px-2 text-slate-600 truncate max-w-xs">{a.message}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
