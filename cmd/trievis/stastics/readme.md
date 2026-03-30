## Merkle分析

root@zkp:/data2# ./trievis --datadir /data2/geth --address 0x77777779B121cCf5CEcda0eDc3502c6085914fa5  --output result.txt

这个可以执行一个合约把它的Storage的merkle树打印出来

Account Hash = keccak256(ContractAddress)

对应的数据库Key-Value存储的是rlp编码的数据, 数据解析出来, 再区分Branch\Extension\Leaf节点类型

[0003] kind=LEAF-PREFIX   path=000204                              hash=0x5e71030768…
       rlp: 0xed9f35cc4d20dbbe0711a9f6cacef13ae8321bee2aae9bfdd75f9c0d66365fb64b8c8b39e7139a8c08fa06000000
       key-segment: 050c0c040d02000d0b0b0e000701010a090f060c0a0c0e0f01030a0e080302010b0e0e020a0a0e090b0f0d0d07050f090c000d06060306050f0b06040b10  →  raw-value: 0x8b39e7139a8c08fa06000000

上面这个例子中, path=000204, 说明这个Leaf下面的Value, 是024大头的, value是024 加上 key-segment 里面的 5 c c 4 d....., 那就是0245cc4d...的value

## 统计结果

trie_depth_by_type.png 表明
Account树中, 大多数的账户在Merkle树中 7 8 9这三层的叶子节点上
Account 合约账户的 Storage 树, 属于Account下面, 在Merkle树中 3 4 5 6 7 8  这几层比较多

trie_storage_max_depth.png表明
多数 Storage 树里面存储的Slot数据, 大多数只有少数的槽位, 1-3层就可以存储下了

简单点描述就是, 大多数合约存储需要找很多层, 才能找到相应的Storage的根, 但是根下面实际存储的数据(Slot), 一般都比较少

而像ERC20的很多合约, 有大量的Slot, 这个时候, 其storage depth就会很深