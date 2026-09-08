<?php

declare(strict_types=1);

namespace Paybridge\Tests;

use Paybridge\IdempotencyKey;
use PHPUnit\Framework\TestCase;

final class IdempotencyKeyTest extends TestCase
{
    public function testGenerateReturnsWellFormedUuidV4(): void
    {
        $key = IdempotencyKey::generate();

        self::assertMatchesRegularExpression(
            '/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i',
            $key,
        );
    }

    public function testGenerateReturnsUniqueValues(): void
    {
        $a = IdempotencyKey::generate();
        $b = IdempotencyKey::generate();

        self::assertNotSame($a, $b);
    }
}
