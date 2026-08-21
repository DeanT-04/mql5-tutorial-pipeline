// golden_e2d5eh3Zi9o.mq5 — ground truth for "LEARN MQL5 TUTORIAL BASICS - 22
// HOW TO CODE AN RSI EXPERT ADVISOR" (https://youtu.be/e2d5eh3Zi9o).
//
// Derivation (transparent): every line below is dictated in the video's audio,
// transcribed in the pipeline's own chunks.json (c0001-c0010):
//   - include Trade.mqh + instance "trade"            (c0002)
//   - Ask/Bid via SymbolInfoDouble + NormalizeDouble/_Digits (c0002-c0003)
//   - variable "signal" (no initial value spoken; assigned "sell"/"buy" later) (c0004, c0007, c0008)
//   - double array myRSIArray                          (c0004)
//   - ArraySetAsSeries on the array                    (c0010)
//   - iRSI with period 14 applied to close prices      (c0004-c0006)
//   - CopyBuffer: buffer 0, start 0, 3 candles         (c0006)
//   - RSI value = myRSIArray[0] cut to two digits       (c0007)
//   - >70 -> sell, <30 -> buy                           (c0007-c0008)
//   - trade.Sell / trade.Buy 10 micro lot = 0.01 when PositionsTotal() < 1 (c0008)
//   - Comment("The signal is now: ", signal)            (c0008)
// The video deletes everything above OnTick ("delete everything above the
// OnTick function and the two comment lines"), so OnInit/OnDeinit are absent.
// Argument order of trade.Sell/Buy/Comment is canonical MQL5 CTrade signature;
// the video names each element ("we want to trade.Sell to sell 10 micro lot").
#property strict

#include <Trade\Trade.mqh>
CTrade trade;

void OnTick()
{
   double Ask = NormalizeDouble(SymbolInfoDouble(_Symbol, SYMBOL_ASK), _Digits);
   double Bid = NormalizeDouble(SymbolInfoDouble(_Symbol, SYMBOL_BID), _Digits);
   string signal = "";
   double myRSIArray[];
   ArraySetAsSeries(myRSIArray, true);
   int RSI_Definition = iRSI(_Symbol, _Period, 14, PRICE_CLOSE);
   CopyBuffer(RSI_Definition, 0, 0, 3, myRSIArray);
   double RSIValue = NormalizeDouble(myRSIArray[0], 2);
   if (RSIValue > 70) signal = "sell";
   if (RSIValue < 30) signal = "buy";
   if (signal == "sell" && PositionsTotal() < 1)
      trade.Sell(0.01, NULL, Bid, 0, 0, NULL);
   if (signal == "buy" && PositionsTotal() < 1)
      trade.Buy(0.01, NULL, Ask, 0, 0, NULL);
   Comment("The signal is now: ", signal);
}
