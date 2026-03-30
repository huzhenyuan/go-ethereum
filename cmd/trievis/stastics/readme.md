## Merkle分析

root@zkp:/data2# ./trievis --datadir /data2/geth --address 0x77777779B121cCf5CEcda0eDc3502c6085914fa5  --output result.txt

这个可以执行一个合约把它的Storage的merkle树打印出来

Account Hash = keccak256(ContractAddress)

对应的数据库Key-Value存储的是rlp编码的数据, 数据解析出来, 再区分Branch\Extension\Leaf节点类型

[0003] kind=LEAF-PREFIX   path=000204                              hash=0x5e71030768…
       rlp: 0xed9f35cc4d20dbbe0711a9f6cacef13ae8321bee2aae9bfdd75f9c0d66365fb64b8c8b39e7139a8c08fa06000000
       key-segment: 050c0c040d02000d0b0b0e000701010a090f060c0a0c0e0f01030a0e080302010b0e0e020a0a0e090b0f0d0d07050f090c000d06060306050f0b06040b10  →  raw-value: 0x8b39e7139a8c08fa06000000

上面这个例子中, path=000204, 说明这个Leaf下面的Value, 是024大头的, value是024 加上 key-segment 里面的 5 c c 4 d....., 那就是0245cc4d...的value



## 单个合约内容统计
root@zkp:~# geth --datadir /data2 db inspect-trie --contract 0x77777779B121cCf5CEcda0eDc3502c6085914fa5
INFO [03-30|16:28:53.529] Maximum peer count                       ETH=50 total=50
INFO [03-30|16:28:53.530] Smartcard socket not found, disabling    err="stat /run/pcscd/pcscd.comm: no such file or directory"
INFO [03-30|16:28:53.533] Set global gas cap                       cap=50,000,000
INFO [03-30|16:28:53.533] Initializing the KZG library             backend=gokzg
INFO [03-30|16:28:53.539] Using pebble as the backing database
INFO [03-30|16:28:53.539] Allocated cache and file handles         database=/data2/geth/chaindata cache=512.00MiB handles=32767
INFO [03-30|16:28:54.586] Opened ancient database                  database=/data2/geth/chaindata/ancient/chain readonly=false
INFO [03-30|16:28:54.586] Opened Era store                         datadir=/data2/geth/chaindata/ancient/chain/era
INFO [03-30|16:28:54.587] State scheme set to already existing     scheme=path
INFO [03-30|16:28:54.587] Load database journal from file          path=/data2/geth/triedb/merkle.journal
INFO [03-30|16:28:56.590] Opened ancient database                  database=/data2/geth/chaindata/ancient/state readonly=true
INFO [03-30|16:28:56.591] Initialized path database                readonly=true  triecache=16.00MiB statecache=16.00MiB buffer=0.00B state-history="entire chain" journal-dir=/data2/geth/triedb
INFO [03-30|16:28:56.591] Inspecting contract                      address=0x77777779B121cCf5CEcda0eDc3502c6085914fa5 root=ebb80f..5dbf67 block=24,731,688

=== Contract Inspection: 0x77777779B121cCf5CEcda0eDc3502c6085914fa5 ===
Account hash: 0xbcb93cd3fbbf7cc1e4968dd1a6a6d62aba3699631d75882756a148f4b2a596e4

Account snapshot: 70.00 B
Snapshot storage: 105 slots (7.95 KiB)
Storage trie:     243 nodes (9.73 KiB)

Storage Trie Depth Distribution:
+-------+-------+------+-------+-------+----------+
| Depth | Short | Full | Value | Nodes |   Size   |
+-------+-------+------+-------+-------+----------+
|   0   |   0   |  1   |   0   |   1   | 532.00 B |
|   1   |   0   |  16  |   0   |  16   | 3.05 KiB |
|   2   |  74   |  14  |   0   |  88   | 4.63 KiB |
|   3   |  30   |  1   |  73   |  104  | 1.44 KiB |
|   4   |   2   |  0   |  30   |  32   | 92.00 B  |
|   5   |   0   |  0   |   2   |   2   |  0.00 B  |

上面表明, 总计243个key:
 1 
16 
88 
104
32 
 2 
叶子节点(Value or Slot)数量105个
73
30
 2

## 全局统计结果

trie_depth_by_type.png 表明
Account树中, 大多数的账户在Merkle树中 7 8 9这三层的叶子节点上
Account 合约账户的 Storage 树, 属于Account下面, 在Merkle树中 3 4 5 6 7 8  这几层比较多

trie_storage_max_depth.png表明
多数 Storage 树里面存储的Slot数据, 大多数只有少数的槽位, 1-3层就可以存储下了

简单点描述就是, 大多数合约存储需要找很多层, 才能找到相应的Storage的根, 但是根下面实际存储的数据(Slot), 一般都比较少

而像ERC20的很多合约, 有大量的Slot, 这个时候, 其storage depth就会很深