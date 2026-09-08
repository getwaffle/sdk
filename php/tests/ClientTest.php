<?php

declare(strict_types=1);

namespace Paybridge\Tests;

use Paybridge\Client;
use Paybridge\Dto\CalculateFeeParams;
use Paybridge\Dto\CreateChargeParams;
use Paybridge\Dto\CreatePayoutParams;
use Paybridge\Dto\RegisterBankAccountParams;
use Paybridge\Enum\ChargeStatus;
use Paybridge\Enum\Mode;
use Paybridge\Enum\PayoutStatus;
use Paybridge\Exception\PaybridgeApiException;
use Paybridge\Http\TransportResponse;
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

    public function testCreateChargeSendsCorrectRequestWithProvider(): void
    {
        $responseBody = json_encode([
            'id' => 'charge-1',
            'provider' => 'xendit',
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
                provider: 'xendit',
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
            'provider' => 'xendit',
            'description' => 'Order #42',
            'customer_ref' => 'cust-42',
            'return_url' => 'https://shop.example/return',
            'expires_in_minutes' => 60,
            'metadata' => ['orderId' => '42'],
        ], $sentBody);

        self::assertSame('charge-1', $charge->id);
        self::assertSame('xendit', $charge->provider);
        self::assertSame(Mode::Sandbox, $charge->mode);
        self::assertSame(ChargeStatus::Pending, $charge->status);
        self::assertSame(100000, $charge->grossAmount);
        self::assertSame(3000, $charge->feeAmount);
        self::assertSame(97000, $charge->netAmount);
        self::assertSame('https://pay.example/charge-1', $charge->checkoutUrl);
        self::assertIsInt($charge->grossAmount);
    }

    public function testCreateChargeOmitsProviderFromBodyWhenNull(): void
    {
        $responseBody = json_encode([
            'id' => 'charge-2',
            'provider' => 'sandbox',
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
        self::assertArrayNotHasKey('provider', $sentBody);
        self::assertArrayNotHasKey('metadata', $sentBody);
        self::assertNull($charge->checkoutUrl);
        self::assertNull($charge->metadata);
    }

    public function testCreateChargeSendsChannelAndVaBankAndMapsQrVaFields(): void
    {
        $responseBody = json_encode([
            'id' => 'charge-va-1',
            'provider' => 'xendit',
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
            'provider' => 'doku',
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
            'provider' => 'xendit',
            'gross_amount' => 100000,
            'fee_amount' => 3000,
            'net_amount' => 97000,
            'currency' => 'IDR',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(200, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $quote = $client->calculateFee(new CalculateFeeParams(provider: 'xendit', amount: 100000, currency: 'IDR'));

        self::assertSame('POST', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/fees/calculate', $transport->lastUrl);
        self::assertArrayNotHasKey('Idempotency-Key', $transport->lastHeaders);

        $sentBody = json_decode((string) $transport->lastBody, true, flags: JSON_THROW_ON_ERROR);
        self::assertSame(['provider' => 'xendit', 'amount' => 100000, 'currency' => 'IDR'], $sentBody);
        self::assertSame(3000, $quote->feeAmount);
    }

    public function testRegisterBankAccountSendsCorrectRequest(): void
    {
        $responseBody = json_encode([
            'id' => 'bank-1',
            'bank_code' => 'BCA',
            'account_number' => '1234567890',
            'account_holder_name' => 'Budi Santoso',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(201, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $account = $client->registerBankAccount(new RegisterBankAccountParams(
            bankCode: 'BCA',
            accountNumber: '1234567890',
            accountHolderName: 'Budi Santoso',
        ));

        self::assertSame('POST', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/bank-accounts', $transport->lastUrl);
        $sentBody = json_decode((string) $transport->lastBody, true, flags: JSON_THROW_ON_ERROR);
        self::assertSame([
            'bank_code' => 'BCA',
            'account_number' => '1234567890',
            'account_holder_name' => 'Budi Santoso',
        ], $sentBody);
        self::assertSame('bank-1', $account->id);
    }

    public function testCreatePayoutSendsCorrectRequest(): void
    {
        $responseBody = json_encode([
            'id' => 'payout-1',
            'bank_account_id' => 'bank-1',
            'provider' => 'xendit',
            'mode' => 'sandbox',
            'status' => 'completed',
            'amount' => 40000,
            'currency' => 'IDR',
        ], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(201, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        $payout = $client->createPayout(
            new CreatePayoutParams(bankAccountId: 'bank-1', provider: 'xendit', amount: 40000, currency: 'IDR'),
            'idem-key-3',
        );

        self::assertSame('POST', $transport->lastMethod);
        self::assertSame(self::BASE_URL . '/v1/payouts', $transport->lastUrl);
        self::assertSame('idem-key-3', $transport->lastHeaders['Idempotency-Key']);
        $sentBody = json_decode((string) $transport->lastBody, true, flags: JSON_THROW_ON_ERROR);
        self::assertSame([
            'bank_account_id' => 'bank-1',
            'provider' => 'xendit',
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
            new CreatePayoutParams(bankAccountId: 'bank-1', provider: 'xendit', amount: 1000, currency: 'IDR'),
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
    public function testNonTwoXxResponseThrowsPaybridgeApiExceptionWithStatusAndMessage(int $status, string $message): void
    {
        $responseBody = json_encode(['error' => $message], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse($status, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        try {
            $client->calculateFee(new CalculateFeeParams(provider: 'xendit', amount: 1000, currency: 'IDR'));
            self::fail('Expected PaybridgeApiException to be thrown');
        } catch (PaybridgeApiException $e) {
            self::assertSame($status, $e->statusCode);
            self::assertSame($message, $e->getMessage());
        }
    }

    public function testHealthzNonTwoXxAlsoThrowsPaybridgeApiException(): void
    {
        $responseBody = json_encode(['error' => 'service unavailable'], JSON_THROW_ON_ERROR);
        $transport = new FakeTransport(new TransportResponse(500, $responseBody));
        $client = new Client(self::API_KEY, self::BASE_URL, $transport);

        try {
            $client->healthz();
            self::fail('Expected PaybridgeApiException to be thrown');
        } catch (PaybridgeApiException $e) {
            self::assertSame(500, $e->statusCode);
            self::assertSame('service unavailable', $e->getMessage());
        }
    }
}
