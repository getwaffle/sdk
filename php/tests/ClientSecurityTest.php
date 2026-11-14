<?php

declare(strict_types=1);

namespace Waffle\Tests;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\TestCase;
use Waffle\Client;
use Waffle\Dto\CreateChargeParams;
use Waffle\Dto\CreatePayoutParams;
use Waffle\Exception\WaffleApiException;
use Waffle\Http\TransportResponse;

final class ClientSecurityTest extends TestCase
{
    private const API_KEY = 'sk_test_secretvalue123';

    private static function fake(int $status = 200, string $body = '{}'): FakeTransport
    {
        return new FakeTransport(new TransportResponse($status, $body));
    }

    /** @return iterable<string,array{string}> */
    public static function validBaseUrls(): iterable
    {
        yield 'default host' => ['https://api.getwaffle.id'];
        yield 'trailing slash' => ['https://api.getwaffle.id/'];
        yield 'port' => ['https://api.example.com:8443'];
        yield 'uppercase scheme' => ['HTTPS://api.getwaffle.id'];
        yield 'http localhost' => ['http://localhost:8080'];
        yield 'http 127.0.0.1' => ['http://127.0.0.1:8080'];
        yield 'http ipv6 loopback' => ['http://[::1]:8080'];
        yield 'https localhost' => ['https://localhost'];
    }

    #[DataProvider('validBaseUrls')]
    public function testAcceptsValidBaseUrl(string $baseUrl): void
    {
        $client = new Client(self::API_KEY, $baseUrl, self::fake());

        self::assertInstanceOf(Client::class, $client);
    }

    /** @return iterable<string,array{string}> */
    public static function invalidBaseUrls(): iterable
    {
        yield 'plain http remote' => ['http://api.getwaffle.id'];
        yield 'http lookalike of localhost' => ['http://localhost.evil.example'];
        yield 'http 127 lookalike' => ['http://127.0.0.1.evil.example'];
        yield 'userinfo' => ['https://user:pass@api.getwaffle.id'];
        yield 'userinfo user only' => ['https://api.getwaffle.id@evil.example'];
        yield 'query' => ['https://api.getwaffle.id?x=1'];
        yield 'empty query marker' => ['https://api.getwaffle.id/?'];
        yield 'fragment' => ['https://api.getwaffle.id#frag'];
        yield 'no host' => ['https:///path'];
        yield 'no scheme' => ['api.getwaffle.id'];
        yield 'ftp scheme' => ['ftp://api.getwaffle.id'];
        yield 'file scheme' => ['file:///etc/passwd'];
        yield 'javascript scheme' => ['javascript:alert(1)'];
        yield 'empty string' => [''];
        yield 'whitespace' => ['https://api.getwaffle.id /x'];
        yield 'newline' => ["https://api.getwaffle.id\r\nX-Evil: 1"];
        yield 'path prefix' => ['https://gateway.example.com/waffle'];
        yield 'multiple slashes' => ['https://api.getwaffle.id///'];
        yield 'backslash' => ['https://evil.example\\@api.getwaffle.id'];
    }

    #[DataProvider('invalidBaseUrls')]
    public function testRejectsInvalidBaseUrl(string $baseUrl): void
    {
        $this->expectException(\InvalidArgumentException::class);
        new Client(self::API_KEY, $baseUrl, self::fake());
    }

    public function testBaseUrlTrailingSlashesAreTrimmed(): void
    {
        $transport = self::fake(200, '{"merchant_id":"m","business_name":"b","mode":"sandbox","preset":"full","scopes":[]}');
        $client = new Client(self::API_KEY, 'https://API.getwaffle.id/', $transport);

        $client->whoAmI();

        self::assertSame('https://api.getwaffle.id/v1/whoami', $transport->lastUrl);
    }

    /** @return iterable<string,array{string}> */
    public static function invalidApiKeys(): iterable
    {
        yield 'empty' => [''];
        yield 'newline' => ["sk_test_abc\nX-Evil: 1"];
        yield 'carriage return' => ["sk_test_abc\rdef"];
        yield 'nul' => ["sk_test_abc\0def"];
        yield 'space' => ['sk_test_abc def'];
        yield 'tab' => ["sk_test_abc\tdef"];
        yield 'trailing space' => ['sk_test_abc '];
        yield 'del' => ["sk_test_abc\x7Fdef"];
    }

    #[DataProvider('invalidApiKeys')]
    public function testRejectsInvalidApiKey(string $apiKey): void
    {
        $this->expectException(\InvalidArgumentException::class);
        new Client($apiKey, 'https://api.getwaffle.id', self::fake());
    }

    public function testApiKeyErrorMessageDoesNotLeakTheKey(): void
    {
        try {
            new Client("sk_test_leaky\nvalue");
            self::fail('expected InvalidArgumentException');
        } catch (\InvalidArgumentException $e) {
            self::assertStringNotContainsString('sk_test_leaky', $e->getMessage());
        }
    }

    /** @return iterable<string,array{string}> */
    public static function badIdempotencyKeys(): iterable
    {
        yield 'crlf injection' => ["abc\r\nX-Evil: 1"];
        yield 'newline' => ["abc\n"];
        yield 'space' => ['abc def'];
        yield 'slash' => ['order/42'];
        yield 'unicode' => ['kunci-é'];
        yield 'too long' => [str_repeat('a', 256)];
    }

    #[DataProvider('badIdempotencyKeys')]
    public function testCreateChargeRejectsBadIdempotencyKey(string $key): void
    {
        $transport = self::fake(201, '{}');
        $client = new Client(self::API_KEY, 'https://api.getwaffle.id', $transport);

        try {
            $client->createCharge(new CreateChargeParams(amount: 1000, currency: 'IDR'), $key);
            self::fail('expected InvalidArgumentException');
        } catch (\InvalidArgumentException) {
            self::assertNull($transport->lastMethod, 'nothing must be sent for an invalid key');
        }
    }

    #[DataProvider('badIdempotencyKeys')]
    public function testCreatePayoutRejectsBadIdempotencyKey(string $key): void
    {
        $transport = self::fake(201, '{}');
        $client = new Client(self::API_KEY, 'https://api.getwaffle.id', $transport);

        try {
            $client->createPayout(new CreatePayoutParams(bankAccountId: 'ba_1', amount: 1000, currency: 'IDR'), $key);
            self::fail('expected InvalidArgumentException');
        } catch (\InvalidArgumentException) {
            self::assertNull($transport->lastMethod, 'nothing must be sent for an invalid key');
        }
    }

    /** @return iterable<string,array{string}> */
    public static function goodIdempotencyKeys(): iterable
    {
        yield 'uuid' => ['3f1c2b6e-8d4a-4c1f-9a77-0e5d2b1c9a10'];
        yield 'order id' => ['order_42:attempt.1'];
        yield 'max length' => [str_repeat('a', 255)];
        yield 'single char' => ['a'];
    }

    #[DataProvider('goodIdempotencyKeys')]
    public function testAcceptsGoodIdempotencyKey(string $key): void
    {
        $transport = self::fake(201, '{}');
        $client = new Client(self::API_KEY, 'https://api.getwaffle.id', $transport);

        try {
            // `@`: the canned `{}` body is not a full charge, so mapping it raises PHP warnings that are irrelevant here.
            @$client->createCharge(new CreateChargeParams(amount: 1000, currency: 'IDR'), $key);
        } catch (\InvalidArgumentException $e) {
            self::fail('valid idempotency key was rejected: ' . $e->getMessage());
        } catch (\Throwable) {
            // The canned `{}` response is not a full charge; only validation matters here.
        }

        self::assertSame($key, $transport->lastHeaders['Idempotency-Key'] ?? null);
    }

    public function testClientCannotBeSerialized(): void
    {
        $client = new Client(self::API_KEY, 'https://api.getwaffle.id', self::fake());

        $this->expectException(\LogicException::class);
        serialize($client);
    }

    public function testClientCannotBeUnserialized(): void
    {
        $this->expectException(\LogicException::class);
        (new Client(self::API_KEY, 'https://api.getwaffle.id', self::fake()))->__unserialize([]);
    }

    public function testDebugOutputIsRedacted(): void
    {
        $client = new Client(self::API_KEY, 'https://api.getwaffle.id', self::fake());

        ob_start();
        var_dump($client);
        $dump = (string) ob_get_clean();
        $printed = print_r($client, true);

        foreach ([$dump, $printed] as $output) {
            self::assertStringNotContainsString('secretvalue123', $output);
            self::assertStringContainsString('sk_test_****', $output);
        }

        $debug = $client->__debugInfo();
        self::assertSame('sk_test_****', $debug['apiKey']);
    }

    public function testDebugInfoForUnknownPrefixShowsOnlyMask(): void
    {
        $client = new Client('wf_other_abcdef', 'https://api.getwaffle.id', self::fake());

        self::assertSame('****', $client->__debugInfo()['apiKey']);
    }

    public function testNonJsonErrorBodyIsTruncatedAndStripsControlCharacters(): void
    {
        $body = "line1\r\nline2\x00" . str_repeat('x', 2000);
        $client = new Client(self::API_KEY, 'https://api.getwaffle.id', self::fake(502, $body));

        try {
            $client->whoAmI();
            self::fail('expected WaffleApiException');
        } catch (WaffleApiException $e) {
            self::assertSame(502, $e->statusCode);
            self::assertLessThanOrEqual(512, strlen($e->getMessage()));
            self::assertSame(0, preg_match('/[\x00-\x1F\x7F]/', $e->getMessage()));
            self::assertStringStartsWith('line1  line2 ', $e->getMessage());
        }
    }

    public function testBidiOverridesAndLineSeparatorsAreStripped(): void
    {
        $body = json_encode(['error' => "a\u{202E}b\u{2028}c\u{200B}d\u{FEFF}e\u{2066}f"], JSON_THROW_ON_ERROR);
        $client = new Client(self::API_KEY, 'https://api.getwaffle.id', self::fake(400, $body));

        try {
            $client->whoAmI();
            self::fail('expected WaffleApiException');
        } catch (WaffleApiException $e) {
            self::assertSame('a b c d e f', $e->getMessage());
        }
    }

    public function testInvalidUtf8AndMultibyteCutAreHandled(): void
    {
        $body = "bad\xFF\xFEbytes" . str_repeat("\u{20AC}", 400);
        $client = new Client(self::API_KEY, 'https://api.getwaffle.id', self::fake(502, $body));

        try {
            $client->whoAmI();
            self::fail('expected WaffleApiException');
        } catch (WaffleApiException $e) {
            self::assertLessThanOrEqual(512, strlen($e->getMessage()));
            self::assertSame(1, preg_match('//u', $e->getMessage()));
        }
    }

    public function testJsonErrorMessageIsAlsoSanitized(): void
    {
        $body = json_encode(['error' => "bad\nthing"], JSON_THROW_ON_ERROR);
        $client = new Client(self::API_KEY, 'https://api.getwaffle.id', self::fake(400, $body));

        try {
            $client->whoAmI();
            self::fail('expected WaffleApiException');
        } catch (WaffleApiException $e) {
            self::assertSame('bad thing', $e->getMessage());
        }
    }
}
