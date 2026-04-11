
#!/bin/bash
set -e

echo "🚀 Setting up development environment..."

# Copy .env.example if .env doesn't exist
if [ ! -f .env ]; then
    echo "📝 Creating .env from .env.example..."
    cp .env.example .env
    echo "✅ .env created. Please review and update values."
else
    echo "ℹ️  .env already exists, skipping..."
fi

# Initialize Pulumi stack
echo "📦 Initializing Pulumi stack..."
pulumi stack init dev --non-interactive || echo "Stack already exists"

# Install Go dependencies
echo "📥 Installing Go dependencies..."
go mod tidy

echo "✅ Development environment setup complete!"
echo ""
echo "Next steps:"
echo "  1. Review and update .env file"
echo "  2. Run: pulumi preview"
echo "  3. Run: pulumi up"