package neolisten

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/astaxie/beego/logs"
	"io/ioutil"
	"math/big"
	"net/http"
	"poly-swap/conf"
	"poly-swap/models"
	"poly-swap/utils"
	"strconv"
)

const (
	_neo_crosschainlock = "CrossChainLockEvent"
	_neo_crosschainunlock = "CrossChainUnlockEvent"
	_neo_lock               = "Lock"
	_neo_lock2              = "LockEvent"
	_neo_unlock             = "UnlockEvent"
	_neo_unlock2            = "Unlock"
)

type NeoChainListen struct {
	neoCfg *conf.NeoChainListenConfig
	neoSdk *NeoSdk
}

func NewNeoChainListen(cfg *conf.NeoChainListenConfig) *NeoChainListen {
	ethListen := &NeoChainListen{}
	ethListen.neoCfg = cfg
	sdk := NewNeoSdk(cfg.RestURL)
	ethListen.neoSdk = sdk
	return ethListen
}

func (this *NeoChainListen) GetLatestHeight() (uint64, error) {
	return this.neoSdk.GetBlockCount()
}

func (this *NeoChainListen) GetBackwardBlockNumber() uint64 {
	return this.neoCfg.BackwardBlockNumber
}

func (this *NeoChainListen) GetChainListenSlot() uint64 {
	return this.neoCfg.ListenSlot
}

func (this *NeoChainListen) GetChainId() uint64 {
	return this.neoCfg.ChainId
}

func (this *NeoChainListen) GetChainName() string {
	return this.neoCfg.ChainName
}

func (this *NeoChainListen) parseNeoMethod(v string) string {
	xx, _ := hex.DecodeString(v)
	return string(xx)
}

func (this *NeoChainListen) HandleNewBlock(height uint64) ([]*models.WrapperTransaction, []*models.SrcTransaction, []*models.PolyTransaction, []*models.DstTransaction, error) {
	block, err := this.neoSdk.GetBlockByIndex(height)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if block == nil {
		return nil, nil, nil, nil, fmt.Errorf("can not get neo block!")
	}
	tt := block.Time
	wrapperTransactions := make([]*models.WrapperTransaction, 0)
	srcTransactions := make([]*models.SrcTransaction, 0)
	dstTransactions := make([]*models.DstTransaction, 0)
	for _, tx := range block.Tx {
		if tx.Type != "InvocationTransaction" {
			continue
		}
		appLog, err := this.neoSdk.GetApplicationLog(tx.Txid)
		if err != nil {
			continue
		}
		for _, exeitem := range appLog.Executions {
			for _, notify := range exeitem.Notifications {
				if notify.Contract[2:] == this.neoCfg.WrapperContract {
					if len(notify.State.Value) <= 0 {
						continue
					}
					contractMethod := this.parseNeoMethod(notify.State.Value[0].Value)
					switch contractMethod {
					case _neo_crosschainlock:
						logs.Info("from chain: %s, txhash: %s\n", this.GetChainName(), tx.Txid[2:])
						if len(notify.State.Value) < 6 {
							continue
						}
						xx, _ := strconv.ParseUint(notify.State.Value[3].Value, 10, 64)
						wrapperTransactions = append(wrapperTransactions, &models.WrapperTransaction{
							Hash:         tx.Txid[2:],
							User:         notify.State.Value[4].Value,
							SrcChainId:   xx,
							DstChainId:   xx,
							FeeTokenHash: notify.State.Value[4].Value,
							FeeAmount:    xx,
						})
					}
				} else if notify.Contract[2:] == this.neoCfg.ProxyContract {
					if len(notify.State.Value) <= 0 {
						continue
					}
					contractMethod := this.parseNeoMethod(notify.State.Value[0].Value)
					switch contractMethod {
					case _neo_crosschainlock:
						logs.Info("from chain: %s, txhash: %s\n", this.GetChainName(), tx.Txid[2:])
						if len(notify.State.Value) < 6 {
							continue
						}
						fctransfer := &models.SrcTransfer{}
						for _, notifynew := range exeitem.Notifications {
							contractMethodNew := this.parseNeoMethod(notifynew.State.Value[0].Value)
							if contractMethodNew == _neo_lock || contractMethodNew == _neo_lock2 {
								if len(notifynew.State.Value) < 7 {
									continue
								}
								fctransfer.Hash = tx.Txid[2:]
								fctransfer.From = utils.Hash2Address(this.GetChainId(), notifynew.State.Value[2].Value)
								fctransfer.To = utils.Hash2Address(this.GetChainId(), notify.State.Value[2].Value)
								fctransfer.Asset = utils.HexStringReverse(notifynew.State.Value[1].Value)
								amount := big.NewInt(0)
								if notifynew.State.Value[6].Type == "Integer" {
									amount, _ = new(big.Int).SetString(notifynew.State.Value[6].Value, 10)
								} else {
									amount, _ = new(big.Int).SetString(utils.HexStringReverse(notifynew.State.Value[6].Value), 16)
								}
								fctransfer.Amount = amount.Uint64()
								tchainId, _ := strconv.ParseUint(notifynew.State.Value[3].Value, 10, 32)
								fctransfer.DstChainId = uint64(tchainId)
								if len(notifynew.State.Value[5].Value) != 40 {
									continue
								}
								fctransfer.DstUser = utils.Hash2Address(uint64(tchainId), notifynew.State.Value[5].Value)
								fctransfer.DstAsset = notifynew.State.Value[4].Value
								break
							}
						}
						fctx := &models.SrcTransaction{}
						fctx.ChainId = this.GetChainId()
						fctx.Hash = tx.Txid[2:]
						fctx.State = 1
						fctx.Fee = uint64(utils.String2Float64(exeitem.GasConsumed))
						fctx.Time = uint64(tt)
						fctx.Height = height
						fctx.User = fctransfer.From
						toChainId, _ := strconv.ParseInt(notify.State.Value[3].Value, 10, 64)
						fctx.DstChainId = uint64(toChainId)
						fctx.Contract = notify.State.Value[2].Value
						fctx.Key = notify.State.Value[4].Value
						fctx.Param = notify.State.Value[5].Value
						fctx.SrcTransfer = fctransfer
						srcTransactions = append(srcTransactions, fctx)
					case _neo_crosschainunlock:
						logs.Info("to chain: %s, txhash: %s\n", this.GetChainName(), tx.Txid[2:])
						if len(notify.State.Value) < 4 {
							continue
						}
						tctransfer := &models.DstTransfer{}
						for _, notifynew := range exeitem.Notifications {
							contractMethodNew := this.parseNeoMethod(notifynew.State.Value[0].Value)
							if contractMethodNew == _neo_unlock || contractMethodNew == _neo_unlock2 {
								if len(notifynew.State.Value) < 4 {
									continue
								}
								tctransfer.Hash = tx.Txid[2:]
								tctransfer.From = utils.Hash2Address(this.GetChainId(), notify.State.Value[2].Value)
								tctransfer.To = utils.Hash2Address(this.GetChainId(), notifynew.State.Value[2].Value)
								tctransfer.Asset = utils.HexStringReverse(notifynew.State.Value[1].Value)
								//amount, _ := strconv.ParseUint(common.HexStringReverse(notifynew.State.Value[3].Value), 16, 64)
								amount := big.NewInt(0)
								if notifynew.State.Value[3].Type == "Integer" {
									amount, _ = new(big.Int).SetString(notifynew.State.Value[3].Value, 10)
								} else {
									amount, _ = new(big.Int).SetString(utils.HexStringReverse(notifynew.State.Value[3].Value), 16)
								}
								tctransfer.Amount = amount.Uint64()
								break
							}
						}
						tctx := &models.DstTransaction{}
						tctx.ChainId = this.GetChainId()
						tctx.Hash = tx.Txid[2:]
						tctx.State = 1
						tctx.Fee = uint64(utils.String2Float64(exeitem.GasConsumed))
						tctx.Time = uint64(tt)
						tctx.Height = height
						fchainId, _ := strconv.ParseUint(notify.State.Value[1].Value, 10, 32)
						tctx.SrcChainId = uint64(fchainId)
						tctx.Contract = utils.HexStringReverse(notify.State.Value[2].Value)
						tctx.PolyHash = utils.HexStringReverse(notify.State.Value[3].Value)
						tctx.DstTransfer = tctransfer
						dstTransactions = append(dstTransactions, tctx)
					default:
						logs.Warn("ignore method: %s", contractMethod)
					}
				}
			}
		}
	}
	return wrapperTransactions, srcTransactions, nil, dstTransactions, nil
}

type ExtendHeight struct {
	last_block_height  uint64  `json:"last_block_height,string"`
}

func (this *NeoChainListen) GetExtendLatestHeight() (uint64, error) {
	req, err := http.NewRequest("GET", this.neoCfg.ExtendNodeURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accepts", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("response status code: %d", resp.StatusCode)
	}
	respBody, _ := ioutil.ReadAll(resp.Body)
	//fmt.Printf("resp body: %s\n", string(respBody))
	extendHeight := new(ExtendHeight)
	err = json.Unmarshal(respBody, extendHeight)
	if err != nil {
		return 0, err
	}
	return extendHeight.last_block_height, nil
}

