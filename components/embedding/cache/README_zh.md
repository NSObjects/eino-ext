# Eino 缓存嵌入器

此模块为 Eino 提供了一个缓存嵌入器，旨在高效地存储和检索嵌入。缓存嵌入器可以通过缓存先前计算的嵌入来加速嵌入过程。

## 安装

```shell
go get github.com/cloudwego/eino-ext/components/embedding/cache
```

## 使用方法

```go
package main

import (
	"context"
	"crypto/md5"
	"log"

	"github.com/cloudwego/eino-ext/components/embedding/cache"
	cacheredis "github.com/cloudwego/eino-ext/components/embedding/cache/redis"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/redis/go-redis/v9"
)

func main() {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// 原始嵌入器，你可以用任何其他嵌入器实现替换它
	// 这只是一个示例，你需要在这里提供一个真实的嵌入器实现
	var originalEmbedder embedding.Embedder
	// embedder, err := openai.NewEmbedder(ctx, &openai.EmbeddingConfig{
	// 	APIKey:     accessKey,
	// }
	// ...

	embedder, err := cache.NewEmbedder(originalEmbedder,
		cache.WithCacher(cacheredis.NewCacher(rdb)),            // 使用 Redis 作为缓存
		cache.WithGenerator(cache.NewHashGenerator(md5.New())), // 使用 md5 生成唯一键
	)
	if err != nil {
		log.Fatal(err)
	}

	embeddings, err := embedder.EmbedStrings(context.Background(), []string{"hello", "how are you"})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("embeddings: %v", embeddings)
}
```

## 功能特性

- **缓存**：缓存嵌入器将嵌入存储在缓存中，以避免对相同输入重新计算。
- **缓存器**：缓存嵌入器支持不同的缓存后端，例如 Redis。
  - 目前支持 [Redis](./redis)。
- **生成器**：缓存嵌入器使用生成器创建用于缓存嵌入的唯一键。
  - 目前支持基于 hash.Hash 接口的简单生成器和哈希生成器。

## 缓存键格式与迁移

内置生成器使用带版本的缓存键。SimpleGenerator 在文本前编码其字节长度，随后拼接文本和模型；HashGenerator 对该编码计算哈希，并给摘要添加版本前缀。这样可以避免文本与模型之间的边界歧义。两种格式均会改变已有缓存键。

升级时请使用**新的缓存命名空间**（Redis 可通过 `cacheredis.WithPrefix` 设置新前缀）。不要回退查询旧键：旧编码存在歧义，可能已经关联了错误向量。旧数据可等待过期，或按运维策略仅清理专用旧命名空间；库不会自动删除旧键。

不同 Embedding 厂商、默认模型、向量维度及其他影响向量的配置应使用独立命名空间。内置生成器只能获取文本和显式传入的模型选项，无法推断底层嵌入器的默认配置。自定义 Generator 不受此次格式调整影响。

## 示例

查看 [examples](./examples/) 目录获取完整的使用示例。
