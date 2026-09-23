<?php

declare(strict_types=1);

namespace Waffle\Tests;

use Waffle\Client;
use Waffle\Dto\CalculateFeeParams;
use Waffle\Dto\CreateChargeParams;
use Waffle\Dto\CreatePayoutParams;
use Waffle\Enum\ApiKeyPreset;
use Waffle\Enum\ChargeStatus;
use Waffle\Enum\Mode;
use Waffle\Enum\PayoutStatus;
use Waffle\Exception\WaffleApiException;
use Waffle\Exception\WafflePermissionException;
use Waffle\Http\TransportResponse;
use PHPUnit\Framework\TestCase;

final class ClientTest extends TestCase
{
    private const API_KEY = 'sk_test_123';
    private const BASE_URL = 'http://localhost:8080';

    public function testConstructorRejectsEmptyApiKey(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        new Client('');
    }

    public function testCreateChargeSendsCorrectRequest(): void
    {
        $responseBody = json_encode([
            'id' => 'charge-1',
            'mode' => 'sandbox',
            'status' => 'pending',
            'gross_amount' => 100000,
            'fee_amount' => 3000,
            'net_amount' => 97000,
            'currency' => 'IDR',
            'checkout_url' => 'https://pay.example/charge-1',
            'created_at' => '2026-09-08T01:00:00Z',
            'metadata' => ['orderId' => '42'],
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(201, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $charge = $client->createCharge(
            new CreateChargeParams(
                amount: 100000,
                currency: 'IDR',
                description: 'Order #42',
                customerRef: 'cust-42',
                returnUrl: 'https://shop.example/return',
                expiresInMinutes: 60,
                metadata: ['orderId' => '42'],
            ),
            'idem-key-1',
        );

        self::assertSame('POST', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/charges', $transport->lastUrl);
        self::assertSame('Bearer ' . self::API_KEY, $transport->lastHeaders['Authorization']);
        self::assertSame('application/json', $transport->lastHeaders['Content-Type']);
        self::assertSame('idem-key-1', $transport->lastHeaders['Idempotency-Key']);

        $sentBody = json_decode((string) $transport->lastBody, true, flags: JSON_THROW_ON_ERROR);
        self::assertSame([
            'amount' => 100000,
            'currency' => 'IDR',
            'description' => 'Order #42',
            'customer_ref' => 'cust-42',
            'return_url' => 'https://shop.example/return',
            'expires_in_minutes' => 60,
            'metadata' => ['orderId' => '42'],
        ], $sentBody);

        self::assertSame('charge-1', $charge->id);
        self::assertSame(Mode::Sandbox, $charge->mode);
        self::assertSame(ChargeStatus::Pending, $charge->status);
        self::assertSame(100000, $charge->grossAmount);
        self::assertSame(3000, $charge->feeAmount);
        self::assertSame(97000, $charge->netAmount);
        self::assertSame('https://pay.example/charge-1', $charge->checkoutUrl);
        self::assertIsInt($charge->grossAmount);
    }

    public function testCreateChargeSendsMinimalRequest(): void
    {
        $responseBody = json_encode([
            'id' => 'charge-2',
            'mode' => 'sandbox',
            'status' => 'pending',
            'gross_amount' => 5000,
            'fee_amount' => 100,
            'net_amount' => 4900,
            'currency' => 'IDR',
            'created_at' => '2026-09-08T01:00:00Z',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(201, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $charge = $client->createCharge(
            new CreateChargeParams(amount: 5000, currency: 'IDR'),
            'idem-key-2',
        );

        $sentBody = json_decode((string) $transport->lastBody, true, flags: JSON_THROW_ON_ERROR);
        self::assertArrayNotHasKey('metadata', $sentBody);
        self::assertNull($charge->checkoutUrl);
        self::assertNull($charge->metadata);
    }

    public function testCreateChargeSendsChannelAndVaBankAndMapsQrVaFields(): void
    {
        $responseBody = json_encode([
            'id' => 'charge-va-1',
            'mode' => 'sandbox',
            'status' => 'pending',
            'gross_amount' => 75000,
            'fee_amount' => 2000,
            'net_amount' => 73000,
            'currency' => 'IDR',
            'channel' => 'virtual_account',
            'va_bank' => 'BCA',
            'va_number' => '8808123456789',
            'created_at' => '2026-09-08T01:00:00Z',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(201, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $charge = $client->createCharge(
            new CreateChargeParams(
                amount: 75000,
                currency: 'IDR',
                channel: 'virtual_account',
                vaBank: 'BCA',
            ),
            'idem-key-va-1',
        );

        $sentBody = json_decode((string) $transport->lastBody, true, flags: JSON_THROW_ON_ERROR);
        self::assertSame([
            'amount' => 75000,
            'currency' => 'IDR',
            'channel' => 'virtual_account',
            'va_bank' => 'BCA',
        ], $sentBody);

        self::assertSame('virtual_account', $charge->channel);
        self::assertSame('BCA', $charge->vaBank);
        self::assertSame('8808123456789', $charge->vaNumber);
        self::assertNull($charge->qrString);
        self::assertNull($charge->checkoutUrl);
    }

    public function testCreateChargeMapsQrStringForQrisChannel(): void
    {
        $responseBody = json_encode([
            'id' => 'charge-qris-1',
            'mode' => 'sandbox',
            'status' => 'pending',
            'gross_amount' => 15000,
            'fee_amount' => 500,
            'net_amount' => 14500,
            'currency' => 'IDR',
            'channel' => 'qris',
            'qr_string' => '00020101021226610014ID.CO.QRIS.WWW',
            'created_at' => '2026-09-08T01:00:00Z',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(201, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $charge = $client->createCharge(
            new CreateChargeParams(amount: 15000, currency: 'IDR', channel: 'qris'),
            'idem-key-qris-1',
        );

        $sentBody = json_decode((string) $transport->lastBody, true, flags: JSON_THROW_ON_ERROR);
        self::assertSame([
            'amount' => 15000,
            'currency' => 'IDR',
            'channel' => 'qris',
        ], $sentBody);

        self::assertSame('qris', $charge->channel);
        self::assertSame('00020101021226610014ID.CO.QRIS.WWW', $charge->qrString);
        self::assertNull($charge->vaBank);
        self::assertNull($charge->vaNumber);
    }

    public function testCreateChargeRequiresIdempotencyKey(): void
    {
        $client = new Client(self::API_KEY, self::BASE_URL, new FakeTransport(new TransportResponse(201, '{}')));

        $this->expectException(\InvalidArgumentException::class);
        $client->createCharge(new CreateChargeParams(amount: 1000, currency: 'IDR'), '');
    }

    public function testCalculateFeeSendsCorrectRequestWithoutIdempotencyHeader(): void
    {
        $responseBody = json_encode([
            'gross_amount' => 100000,
            'fee_amount' => 3000,
            'net_amount' => 97000,
            'currency' => 'IDR',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $quote = $client->calculateFee(new CalculateFeeParams(amount: 100000, currency: 'IDR'));

        self::assertSame('POST', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/fees/calculate', $transport->lastUrl);
        self::assertArrayNotHasKey('Idempotency-Key', $transport->lastHeaders);

        $sentBody = json_decode((string) $transport->lastBody, true, flags: JSON_THROW_ON_ERROR);
        self::assertSame(['amount' => 100000, 'currency' => 'IDR'], $sentBody);
        self::assertSame(3000, $quote->feeAmount);
    }

    public function testGetBankAccountSendsCorrectRequest(): void
    {
        $responseBody = json_encode([
            'id' => 'bank-1',
            'bank_code' => 'BCA',
            'account_number' => '••••7890',
            'account_holder_name' => 'Budi Santoso',
            'created_at' => '2026-09-08T01:00:00Z',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $account = $client->getBankAccount();

        self::assertSame('GET', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/bank-accounts', $transport->lastUrl);
        self::assertSame('bank-1', $account->id);
        self::assertSame('••••7890', $account->accountNumber);
        self::assertSame('2026-09-08T01:00:00Z', $account->createdAt);
    }

    public function testCreatePayoutSendsCorrectRequest(): void
    {
        $responseBody = json_encode([
            'id' => 'payout-1',
            'bank_account_id' => 'bank-1',
            'mode' => 'sandbox',
            'status' => 'completed',
            'amount' => 40000,
            'currency' => 'IDR',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(201, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $payout = $client->createPayout(
            new CreatePayoutParams(bankAccountId: 'bank-1', amount: 40000, currency: 'IDR'),
            'idem-key-3',
        );

        self::assertSame('POST', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/payouts', $transport->lastUrl);
        self::assertSame('idem-key-3', $transport->lastHeaders['Idempotency-Key']);
        $sentBody = json_decode((string) $transport->lastBody, true, flags: JSON_THROW_ON_ERROR);
        self::assertSame([
            'bank_account_id' => 'bank-1',
            'amount' => 40000,
            'currency' => 'IDR',
        ], $sentBody);
        self::assertSame(PayoutStatus::Completed, $payout->status);
        self::assertNull($payout->failureReason);
    }

    public function testCreatePayoutRequiresIdempotencyKey(): void
    {
        $client = new Client(self::API_KEY, self::BASE_URL, new FakeTransport(new TransportResponse(201, '{}')));

        $this->expectException(\InvalidArgumentException::class);
        $client->createPayout(
            new CreatePayoutParams(bankAccountId: 'bank-1', amount: 1000, currency: 'IDR'),
            '',
        );
    }

    public function testGetBalanceOmitsQueryParamWhenCurrencyNull(): void
    {
        $responseBody = json_encode(['currency' => 'IDR', 'amount' => 287500], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $balance = $client->getBalance();

        self::assertSame('GET', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/balance', $transport->lastUrl);
        self::assertArrayNotHasKey('Content-Type', $transport->lastHeaders);
        self::assertSame(287500, $balance->amount);
    }

    public function testGetBalanceIncludesCurrencyQueryParamWhenGiven(): void
    {
        $responseBody = json_encode(['currency' => 'USD', 'amount' => 100], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $client->getBalance('USD');

        self::assertSame(self::BASE_URL . '/v1/balance?currency=USD', $transport->lastUrl);
    }

    public function testHealthzReturnsTrueOnOkBody(): void
    {
        $transport = new FakeTransport(new TransportResponse(200, 'ok'));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        self::assertTrue($client->healthz());
        self::assertSame('GET', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/healthz', $transport->lastUrl);
    }

    public function testHealthzReturnsFalseOnOtherBody(): void
    {
        $transport = new FakeTransport(new TransportResponse(200, 'degraded'));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        self::assertFalse($client->healthz());
    }

    /** @return array<string,array{int,string}> */
    public static function errorStatusProvider(): array
    {
        return [
            '401 unauthorized' => [401, 'invalid api key'],
            '422 business rule' => [422, 'no fee rule configured'],
            '429 rate limited' => [429, 'fraud velocity limit exceeded'],
            '500 internal error' => [500, 'internal server error'],
        ];
    }

    /** @dataProvider errorStatusProvider */
    public function testNonTwoXxResponseThrowsWaffleApiExceptionWithStatusAndMessage(int $status, string $message): void
    {
        $responseBody = json_encode(['error' => $message], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse($status, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        try {
            $client->calculateFee(new CalculateFeeParams(amount: 1000, currency: 'IDR'));
            self::fail('Expected WaffleApiException to be thrown');
        } catch (WaffleApiException $e) {
            self::assertSame($status, $e->statusCode);
            self::assertSame($message, $e->getMessage());
        }
    }

    public function testHealthzNonTwoXxAlsoThrowsWaffleApiException(): void
    {
        $responseBody = json_encode(['error' => 'service unavailable'], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(500, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        try {
            $client->healthz();
            self::fail('Expected WaffleApiException to be thrown');
        } catch (WaffleApiException $e) {
            self::assertSame(500, $e->statusCode);
            self::assertSame('service unavailable', $e->getMessage());
        }
    }

    public function test403LackingScopeThrowsWafflePermissionExceptionWithParsedScope(): void
    {
        $responseBody = json_encode(['error' => 'this API key lacks the payouts:write permission'], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(403, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        try {
            $client->createPayout(
                new CreatePayoutParams(bankAccountId: 'bank-1', amount: 1000, currency: 'IDR'),
                'idem-key-4',
            );
            self::fail('Expected WafflePermissionException to be thrown');
        } catch (WafflePermissionException $e) {
            self::assertSame(403, $e->statusCode);
            self::assertSame('payouts:write', $e->scope);
            self::assertInstanceOf(WaffleApiException::class, $e);
        }
    }

    public function test403WithUnrecognizedShapeFallsBackToPlainWaffleApiException(): void
    {
        $responseBody = json_encode(['error' => 'forbidden'], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(403, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        try {
            $client->getBalance();
            self::fail('Expected WaffleApiException to be thrown');
        } catch (WafflePermissionException $e) {
            self::fail('Did not expect a WafflePermissionException: ' . $e->getMessage());
        } catch (WaffleApiException $e) {
            self::assertSame(403, $e->statusCode);
            self::assertSame('forbidden', $e->getMessage());
        }
    }

    public function testGetChargeSendsCorrectRequestAndParsesBreakdownAndTimeline(): void
    {
        $responseBody = json_encode([
            'id' => 'charge-1',
            'mode' => 'sandbox',
            'status' => 'paid',
            'gross_amount' => 100000,
            'fee_amount' => 3000,
            'net_amount' => 97000,
            'currency' => 'IDR',
            'created_at' => '2026-09-08T01:00:00Z',
            'paid_at' => '2026-09-08T01:05:00Z',
            'settled_at' => '2026-09-09T01:05:00Z',
            'breakdown' => [
                'base_amount' => 100000,
                'fee_amount' => 3000,
                'fee_bearer' => 'merchant',
                'fee_rule' => ['type' => 'percentage', 'percent_bps' => 250, 'flat_amount' => 500],
                'payer_paid' => 100000,
                'merchant_receives' => 97000,
                'channel' => 'qris',
                'va_bank' => '',
            ],
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $charge = $client->getCharge('charge-1');

        self::assertSame('GET', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/charges/charge-1', $transport->lastUrl);
        self::assertSame('2026-09-08T01:05:00Z', $charge->paidAt);
        self::assertNull($charge->expiresAt);
        self::assertSame('2026-09-09T01:05:00Z', $charge->settledAt);
        self::assertNotNull($charge->breakdown);
        self::assertSame(100000, $charge->breakdown->baseAmount);
        self::assertSame('merchant', $charge->breakdown->feeBearer);
        self::assertNotNull($charge->breakdown->feeRule);
        self::assertSame('percentage', $charge->breakdown->feeRule->type);
        self::assertSame(250, $charge->breakdown->feeRule->percentBps);
    }

    public function testListChargesSendsCorrectQueryAndParsesPage(): void
    {
        $responseBody = json_encode([
            'data' => [[
                'id' => 'charge-1',
                'mode' => 'sandbox',
                'status' => 'paid',
                'gross_amount' => 100000,
                'fee_amount' => 3000,
                'net_amount' => 97000,
                'currency' => 'IDR',
                'created_at' => '2026-09-08T01:00:00Z',
            ]],
            'has_more' => true,
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $page = $client->listCharges(
            limit: 10,
            startingAfter: 'charge-0',
            status: ChargeStatus::Paid,
            createdGte: '2026-01-01',
            createdLte: '2026-12-31',
        );

        self::assertSame(
            self::BASE_URL . '/v1/charges?limit=10&starting_after=charge-0&status=paid'
                . '&created%5Bgte%5D=2026-01-01&created%5Blte%5D=2026-12-31',
            $transport->lastUrl,
        );
        self::assertTrue($page->hasMore);
        self::assertCount(1, $page->data);
        self::assertSame('charge-1', $page->data[0]->id);
    }

    public function testListAllChargesIteratesAcrossPages(): void
    {
        $pageOne = json_encode([
            'data' => [
                ['id' => 'charge-1', 'mode' => 'sandbox', 'status' => 'paid', 'gross_amount' => 1000,
                    'fee_amount' => 0, 'net_amount' => 1000, 'currency' => 'IDR', 'created_at' => '2026-01-01T00:00:00Z'],
                ['id' => 'charge-2', 'mode' => 'sandbox', 'status' => 'paid', 'gross_amount' => 2000,
                    'fee_amount' => 0, 'net_amount' => 2000, 'currency' => 'IDR', 'created_at' => '2026-01-02T00:00:00Z'],
            ],
            'has_more' => true,
        ], JSON_THROW_ON_ERROR);
        $pageTwo = json_encode([
            'data' => [
                ['id' => 'charge-3', 'mode' => 'sandbox', 'status' => 'paid', 'gross_amount' => 3000,
                    'fee_amount' => 0, 'net_amount' => 3000, 'currency' => 'IDR', 'created_at' => '2026-01-03T00:00:00Z'],
            ],
            'has_more' => false,
        ], JSON_THROW_ON_ERROR);

        $transport = new SequencedTransport([
            new TransportResponse(200, $pageOne),
            new TransportResponse(200, $pageTwo),
        ]);
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $ids = [];
        foreach ($client->listAllCharges(pageSize: 2) as $charge) {
            $ids[] = $charge->id;
        }

        self::assertSame(['charge-1', 'charge-2', 'charge-3'], $ids);
        self::assertCount(2, $transport->urls);
        self::assertStringNotContainsString('starting_after', $transport->urls[0]);
        self::assertStringContainsString('starting_after=charge-2', $transport->urls[1]);
    }

    public function testGetPayoutSendsCorrectRequest(): void
    {
        $responseBody = json_encode([
            'id' => 'payout-1',
            'bank_account_id' => 'bank-1',
            'mode' => 'sandbox',
            'status' => 'completed',
            'amount' => 40000,
            'currency' => 'IDR',
            'bank_code' => 'BCA',
            'account_number' => '1234567890',
            'account_holder_name' => 'Budi Santoso',
            'created_at' => '2026-09-08T01:00:00Z',
            'completed_at' => '2026-09-08T01:05:00Z',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $payout = $client->getPayout('payout-1');

        self::assertSame(self::BASE_URL . '/v1/payouts/payout-1', $transport->lastUrl);
        self::assertSame('1234567890', $payout->accountNumber);
        self::assertSame('2026-09-08T01:05:00Z', $payout->completedAt);
    }

    public function testListPayoutsSendsCorrectQuery(): void
    {
        $responseBody = json_encode(['data' => [], 'has_more' => false], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $page = $client->listPayouts(status: PayoutStatus::Held);

        self::assertSame(self::BASE_URL . '/v1/payouts?status=held', $transport->lastUrl);
        self::assertFalse($page->hasMore);
    }

    public function testGetChargeReceiptReturnsRawBytesNotJsonDecoded(): void
    {
        $transport = new FakeTransport(new TransportResponse(200, '%PDF-1.4 fake bytes'));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $pdf = $client->getChargeReceipt('charge-1');

        self::assertSame(self::BASE_URL . '/v1/charges/charge-1/receipt.pdf', $transport->lastUrl);
        self::assertSame('%PDF-1.4 fake bytes', $pdf);
    }

    public function testGetPayoutReceiptThrowsOnConflict(): void
    {
        $responseBody = json_encode(['error' => 'dokumen hanya tersedia untuk pembayaran yang berhasil'], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(409, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $this->expectException(WaffleApiException::class);
        $client->getPayoutReceipt('payout-1');
    }

    public function testWhoAmISendsCorrectRequestAndParsesResponse(): void
    {
        $responseBody = json_encode([
            'merchant_id' => 'merchant-1',
            'business_name' => 'Warung Budi',
            'mode' => 'sandbox',
            'preset' => 'accept_payments',
            'scopes' => ['charges:read', 'charges:write', 'balance:read'],
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $whoAmI = $client->whoAmI();

        self::assertSame('GET', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/whoami', $transport->lastUrl);
        self::assertSame('merchant-1', $whoAmI->merchantId);
        self::assertSame(Mode::Sandbox, $whoAmI->mode);
        self::assertSame(ApiKeyPreset::AcceptPayments, $whoAmI->preset);
        self::assertSame(['charges:read', 'charges:write', 'balance:read'], $whoAmI->scopes);
    }

    public function testListBanksParsesBareArrayResponse(): void
    {
        $responseBody = json_encode([
            ['code' => 'BCA', 'name' => 'Bank Central Asia', 'logo_url' => '/bank-logos/BCA.svg', 'sort_order' => 1],
            ['code' => 'BNI', 'name' => 'Bank Negara Indonesia', 'logo_url' => null, 'sort_order' => 2],
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $banks = $client->listBanks();

        self::assertSame(self::BASE_URL . '/v1/banks', $transport->lastUrl);
        self::assertCount(2, $banks);
        self::assertSame('BCA', $banks[0]->code);
        self::assertSame('/bank-logos/BCA.svg', $banks[0]->logoUrl);
        self::assertNull($banks[1]->logoUrl);
    }
}
