const path = require('path');

const PLUGIN_ID = 'com.bednarz.scheduler';

module.exports = {
    entry: './src/index.tsx',
    resolve: {
        extensions: ['.ts', '.tsx', '.js', '.jsx'],
    },
    module: {
        rules: [
            {
                test: /\.(t|j)sx?$/,
                exclude: /node_modules/,
                use: {
                    loader: 'babel-loader',
                    options: {
                        presets: [
                            ['@babel/preset-env', {targets: {chrome: '90'}}],
                            ['@babel/preset-react', {runtime: 'classic'}],
                            '@babel/preset-typescript',
                        ],
                    },
                },
            },
            {
                test: /\.css$/,
                use: ['style-loader', 'css-loader'],
            },
        ],
    },
    externals: {
        react: 'React',
        'react-dom': 'ReactDOM',
        'react-redux': 'ReactRedux',
        redux: 'Redux',
    },
    output: {
        devtoolNamespace: PLUGIN_ID,
        path: path.resolve(__dirname, 'dist'),
        publicPath: '/',
        filename: 'main.js',
    },
};
