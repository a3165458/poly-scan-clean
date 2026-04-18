export interface PricePoint {
  price: number;
  timestamp: string;
}

export interface TechnicalIndicators {
  spot_price: number;
  rsi: number;
  macd: number;
  macd_signal: number;
  macd_hist: number;
  bollinger_up: number;
  bollinger_mid: number;
  bollinger_low: number;
  bollinger_pct: number;
  atr: number;
  momentum: number;
  trend: string;
  trend_strength: number;
  timestamp: string;
}

export interface MarketInfo {
  id: string;
  question: string;
  up_token_id: string;
  down_token_id: string;
  window_start: string;
  window_end: string;
  time_left: string;
  status: string;
}

export interface PositionInfo {
  market_id: string;
  market_name: string;
  token_id: string;
  side: string;
  entry_price: number;
  current_price: number;
  size: number;
  current_value: number;
  pnl: number;
  pnl_pct: number;
  open_time: string;
  duration: string;
  is_active: boolean;
  close_reason: string;
}

export interface TradeInfo {
  id: string;
  timestamp: string;
  market_id: string;
  market_name: string;
  direction: string;
  side: string;
  price: number;
  size: number;
  total: number;
  confidence: number;
  order_id: string;
  status: string;
  pnl: number;
}

export interface AlertInfo {
  id: string;
  timestamp: string;
  type: string;
  level: string;
  message: string;
  details?: string;
}

export interface StrategyState {
  name: string;
  status: string;
  start_time: string;
  uptime: string;
  total_trades: number;
  winning_trades: number;
  losing_trades: number;
  win_rate: number;
  total_pnl: number;
  daily_pnl: number;
  daily_trades: number;
  consecutive_wins: number;
  consecutive_loss: number;
  drawdown: number;
  in_cooldown: boolean;
}

export interface DashboardData {
  strategy: StrategyState;
  indicators: TechnicalIndicators;
  markets: MarketInfo[];
  positions: PositionInfo[];
  recent_trades: TradeInfo[];
  alerts: AlertInfo[];
  price_history: PricePoint[];
}

export interface StrategyConfig {
  min_confidence: number;
  min_price_change: number;
  max_position_usd: number;
  predict_before_end: number;
  execution_lead_time: number;
  cooldown_per_market: number;
  use_dynamic_pricing: boolean;
  price_slippage: number;
  enable_risk_mgmt: boolean;
  max_daily_loss: number;
  max_drawdown_pct: number;
  max_consecutive_loss: number;
  max_daily_trades: number;
}

export interface PerformanceStats {
  total_trades: number;
  winning_trades: number;
  losing_trades: number;
  win_rate: number;
  total_pnl: number;
  average_pnl: number;
  best_trade: number;
  worst_trade: number;
  claim_attempts: number;
  claim_successes: number;
  claim_success_rate: number;
  average_hold_time: string;
  max_drawdown: number;
  sharpe_ratio: number;
  last_updated: string;
  start_time: string;
  uptime: string;
}

export interface NotificationEvent {
  type: string;
  timestamp: string;
  market_id?: string;
  message: string;
  level: 'info' | 'warning' | 'error' | 'success';
  data?: Record<string, unknown>;
}

export interface NotificationConfig {
  enabled: boolean;
  webhook_url: string;
  telegram_token: string;
  telegram_chat_id: string;
  min_level: string;
  rate_limit: number;
  quiet_hours: string;
}

export interface CostStats {
  total_cost: number;
  today_cost: number;
  monthly_cost: number;
  total_requests: number;
  today_requests: number;
  monthly_requests: number;
  by_provider: Record<string, ProviderCostStats>;
}

export interface ProviderCostStats {
  total_cost: number;
  request_count: number;
  avg_cost_per_req: number;
}

export interface DailyCost {
  date: string;
  total_cost: number;
  request_count: number;
  by_provider: Record<string, number>;
}

export interface AccountInfo {
  wallet_address: string;
  usdc_balance: number;
  positions_value: number;
  portfolio_value: number;
  daily_stats: DailyStatInfo[];
  equity_curve: EquityPointInfo[];
}

export interface DailyStatInfo {
  date: string;
  trades: number;
  wins: number;
  losses: number;
  pnl: number;
  win_rate: number;
}

export interface EquityPointInfo {
  date: string;
  value: number;
}
